package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"myapp/internal/app"
	"myapp/internal/clipboard"
	"myapp/internal/config"
	"myapp/internal/fileclipboard"
	"myapp/internal/identity"
	common "myapp/internal/input"
	"myapp/internal/platform"
	"myapp/internal/protocol"
	"myapp/internal/transfer"
	"myapp/internal/transport"
	"myapp/internal/trust"
)

func runServe(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("serve", flag.ContinueOnError)
	set.SetOutput(stderr)
	configPath := set.String("config", defaultConfigPath(), "配置文件路径")
	identityDirectory := set.String("identity-dir", defaultIdentityDir(), "身份目录")
	trustDirectory := set.String("trust-dir", defaultTrustDir(), "信任目录")
	if err := set.Parse(args); err != nil {
		return err
	}
	cfg, localIdentity, store, err := loadRuntime(*configPath, *identityDirectory, *trustDirectory)
	if err != nil {
		return err
	}
	if cfg.Role != config.RoleAgent {
		return errors.New("serve 第一版必须在 agent 配置上运行")
	}
	address, err := net.ResolveTCPAddr("tcp", cfg.ListenAddress)
	if err != nil {
		return err
	}
	listener, err := net.ListenTCP("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	fmt.Fprintln(stdout, "MyApp agent 正在监听", listener.Addr())
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		err = serveConnection(ctx, tls.Server(connection, transport.ServerTLS(localIdentity, store)), listener, store, cfg, localIdentity, stdout)
		_ = connection.Close()
		if err != nil && ctx.Err() == nil {
			fmt.Fprintln(stderr, "会话结束:", err)
		}
	}
}

func serveConnection(ctx context.Context, connection *tls.Conn, listener *net.TCPListener, store trust.Store, cfg config.Config, localIdentity identity.Identity, stdout io.Writer) error {
	var peerID string
	var hello, remote transport.SessionHello
	err := withHandshakeDeadline(ctx, connection, 5*time.Second, func() error {
		if err := connection.HandshakeContext(ctx); err != nil {
			return err
		}
		var err error
		peerID, err = transport.PeerDeviceID(connection.ConnectionState())
		if err != nil {
			return err
		}
		hello, err = transport.NewSessionHello(localIdentity, cfg.Role, transport.ChannelRealtime, time.Now())
		if err != nil {
			return err
		}
		remote, err = transport.ExchangeHello(connection, hello, peerID, false, time.Now())
		return err
	})
	if err != nil {
		return err
	}
	if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	rawBulk, err := listener.Accept()
	resetErr := listener.SetDeadline(time.Time{})
	if err != nil {
		return err
	}
	defer rawBulk.Close()
	if resetErr != nil {
		return resetErr
	}
	bulk := tls.Server(rawBulk, transport.ServerTLS(localIdentity, store))
	if err := negotiateBulk(ctx, bulk, hello, remote, false); err != nil {
		return err
	}
	injector, err := platform.NewInjector()
	if err != nil {
		return err
	}
	defer injector.Close()
	fmt.Fprintln(stdout, "已连接控制端", peerID)
	return runConnected(ctx, connection, bulk, localIdentity.DeviceID, injector, cfg)
}

func runControl(args []string, stdout, stderr io.Writer) error {
	set := flag.NewFlagSet("control", flag.ContinueOnError)
	set.SetOutput(stderr)
	configPath := set.String("config", defaultConfigPath(), "配置文件路径")
	identityDirectory := set.String("identity-dir", defaultIdentityDir(), "身份目录")
	trustDirectory := set.String("trust-dir", defaultTrustDir(), "信任目录")
	peerID := set.String("peer", "", "目标设备 ID")
	address := set.String("addr", "", "目标地址")
	if err := set.Parse(args); err != nil {
		return err
	}
	if (*peerID == "") != (*address == "") {
		return errors.New("peer 和 addr 必须一起提供")
	}
	cfg, localIdentity, store, err := loadRuntime(*configPath, *identityDirectory, *trustDirectory)
	if err != nil {
		return err
	}
	if cfg.Role != config.RoleController {
		return errors.New("control 必须在 controller 配置上运行")
	}
	if *peerID != "" {
		cfg.Peers = []config.Peer{{ID: *peerID, Address: *address}}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if len(cfg.Peers) == 0 {
		return errors.New("请在配置中填写 peers，或提供 peer 和 addr")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runController(ctx, cfg, localIdentity, store, stdout)
}

func runConnected(ctx context.Context, connection net.Conn, bulk io.ReadWriteCloser, deviceID string, injector common.Injector, cfg config.Config) error {
	link, clipboardSync, fileSync, err := buildLink(deviceID, injector, cfg)
	if err != nil {
		return err
	}
	if err := withHandshakeDeadline(ctx, connection, 20*time.Second, func() error {
		frame, err := (protocol.Decoder{MaxPayload: 8}).Decode(connection)
		if err != nil {
			return err
		}
		_, err = transport.ParseHeartbeat(frame)
		return err
	}); err != nil {
		return err
	}
	workers := []func(context.Context) error{func(ctx context.Context) error { return link.RunChannels(ctx, connection, bulk) }}
	if clipboardSync != nil {
		workers = append(workers, clipboardSync.Run)
	}
	if fileSync != nil {
		workers = append(workers, fileSync.Run)
	}
	return runSession(ctx, workers...)
}

func buildLink(deviceID string, injector common.Injector, cfg config.Config) (*app.Link, *clipboard.Synchronizer, *fileclipboard.Synchronizer, error) {
	var link *app.Link
	var syncer *clipboard.Synchronizer
	fileBackend, err := platform.NewFileClipboard()
	if err != nil {
		return nil, nil, nil, err
	}
	if cfg.Clipboard.TextEnabled {
		backend, err := platform.NewClipboard()
		if err != nil {
			return nil, nil, nil, err
		}
		engine, err := clipboard.NewEngine(deviceID)
		if err != nil {
			return nil, nil, nil, err
		}
		syncer, err = clipboard.NewSynchronizer(engine, clipboard.TextOnlyBackend{Backend: backend, Files: fileBackend}, func(ctx context.Context, entry clipboard.Entry) error {
			return link.PublishClipboard(ctx, entry)
		}, 150*time.Millisecond)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	link, err = app.NewSplitLink(injector, syncer)
	if err != nil {
		return nil, nil, nil, err
	}
	var fileSync *fileclipboard.Synchronizer
	if cfg.Files.Enabled {
		fileSync, err = fileclipboard.NewSynchronizer(fileBackend, func(ctx context.Context, transferID string, paths []string) error {
			sources := make([]transfer.Source, len(paths))
			for index, path := range paths {
				sources[index] = transfer.Source{Path: path}
			}
			return link.SendFiles(ctx, transferID, sources)
		}, 250*time.Millisecond)
		if err != nil {
			return nil, nil, nil, err
		}
		if err := link.EnableTransfers(cfg.Files.Cache, fileSync.Publish); err != nil {
			return nil, nil, nil, err
		}
	}
	return link, syncer, fileSync, nil
}

func loadRuntime(configPath, identityDirectory, trustDirectory string) (config.Config, identity.Identity, trust.Store, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, identity.Identity{}, trust.Store{}, err
	}
	localIdentity, err := identity.Load(identityDirectory)
	if err != nil {
		return config.Config{}, identity.Identity{}, trust.Store{}, err
	}
	if cfg.Files.Enabled && !filepath.IsAbs(cfg.Files.Cache) {
		cfg.Files.Cache = filepath.Join(filepath.Dir(configPath), cfg.Files.Cache)
	}
	return cfg, localIdentity, trust.New(trustDirectory), nil
}

func newEmergencyKeys(cancel context.CancelFunc) func(common.Event) bool {
	pressed := make(map[uint16]bool)
	return func(event common.Event) bool {
		switch event.Kind {
		case common.KeyDown:
			pressed[event.Code] = true
		case common.KeyUp:
			delete(pressed, event.Code)
		}
		control := pressed[224] || pressed[228]
		alt := pressed[226] || pressed[230]
		if control && alt && pressed[41] {
			cancel()
			return true
		}
		return false
	}
}
