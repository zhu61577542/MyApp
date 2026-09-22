package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
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
	listener, err := tls.Listen("tcp", cfg.ListenAddress, transport.ServerTLS(localIdentity, store))
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
		err = serveConnection(ctx, connection.(*tls.Conn), cfg, localIdentity, stdout)
		_ = connection.Close()
		if err != nil && ctx.Err() == nil {
			fmt.Fprintln(stderr, "会话结束:", err)
		}
	}
}

func serveConnection(ctx context.Context, connection *tls.Conn, cfg config.Config, localIdentity identity.Identity, stdout io.Writer) error {
	if err := connection.HandshakeContext(ctx); err != nil {
		return err
	}
	peerID, err := transport.PeerDeviceID(connection.ConnectionState())
	if err != nil {
		return err
	}
	hello, err := transport.NewSessionHello(localIdentity, cfg.Role, transport.ChannelControl, time.Now())
	if err != nil {
		return err
	}
	if _, err := transport.ExchangeHello(connection, hello, peerID, false, time.Now()); err != nil {
		return err
	}
	injector, err := platform.NewInjector()
	if err != nil {
		return err
	}
	defer injector.Close()
	fmt.Fprintln(stdout, "已连接控制端", peerID)
	return runConnected(ctx, connection, localIdentity.DeviceID, injector, cfg)
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
	if *peerID == "" || *address == "" {
		return errors.New("peer 和 addr 不能为空")
	}
	cfg, localIdentity, store, err := loadRuntime(*configPath, *identityDirectory, *trustDirectory)
	if err != nil {
		return err
	}
	if cfg.Role != config.RoleController {
		return errors.New("control 必须在 controller 配置上运行")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dialer := tls.Dialer{Config: transport.ClientTLS(localIdentity, *peerID, store)}
	raw, err := dialer.DialContext(ctx, "tcp", *address)
	if err != nil {
		return err
	}
	connection := raw.(*tls.Conn)
	defer connection.Close()
	hello, err := transport.NewSessionHello(localIdentity, cfg.Role, transport.ChannelControl, time.Now())
	if err != nil {
		return err
	}
	if _, err := transport.ExchangeHello(connection, hello, *peerID, true, time.Now()); err != nil {
		return err
	}
	capturer, err := platform.NewCapturer()
	if err != nil {
		return err
	}
	defer capturer.Close()
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	link, clipboardSync, fileSync, err := buildLink(localIdentity.DeviceID, nil, cfg)
	if err != nil {
		return err
	}
	done := make(chan error, 3)
	go func() { done <- link.Run(sessionCtx, connection) }()
	if clipboardSync != nil {
		go func() { done <- clipboardSync.Run(sessionCtx) }()
	}
	if fileSync != nil {
		go func() { done <- fileSync.Run(sessionCtx) }()
	}
	sessionEnd := make(chan error, 1)
	go func() {
		err := <-done
		sessionEnd <- err
		cancel()
	}()
	fmt.Fprintln(stdout, "已控制目标；按 Ctrl+Alt+Esc 或终止程序收回本地控制")
	emergency := newEmergencyKeys(cancel)
	captureErr := capturer.Capture(sessionCtx, func(event common.Event) error {
		if emergency(event) {
			return nil
		}
		return link.SendInput(sessionCtx, event)
	})
	_ = link.SendReleaseAll(context.Background())
	cancel()
	if captureErr != nil && !errors.Is(captureErr, context.Canceled) {
		return captureErr
	}
	select {
	case err := <-sessionEnd:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	default:
	}
	return nil
}

func runConnected(ctx context.Context, connection io.ReadWriter, deviceID string, injector common.Injector, cfg config.Config) error {
	link, clipboardSync, fileSync, err := buildLink(deviceID, injector, cfg)
	if err != nil {
		return err
	}
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 3)
	go func() { done <- link.Run(sessionCtx, connection) }()
	if clipboardSync != nil {
		go func() { done <- clipboardSync.Run(sessionCtx) }()
	}
	if fileSync != nil {
		go func() { done <- fileSync.Run(sessionCtx) }()
	}
	err = <-done
	cancel()
	return err
}

func buildLink(deviceID string, injector common.Injector, cfg config.Config) (*app.Link, *clipboard.Synchronizer, *fileclipboard.Synchronizer, error) {
	var link *app.Link
	var syncer *clipboard.Synchronizer
	if cfg.Clipboard.TextEnabled {
		backend, err := platform.NewClipboard()
		if err != nil {
			return nil, nil, nil, err
		}
		engine, err := clipboard.NewEngine(deviceID)
		if err != nil {
			return nil, nil, nil, err
		}
		syncer, err = clipboard.NewSynchronizer(engine, backend, func(ctx context.Context, entry clipboard.Entry) error {
			return link.PublishClipboard(ctx, entry)
		}, 150*time.Millisecond)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	var err error
	link, err = app.NewLink(injector, syncer)
	if err != nil {
		return nil, nil, nil, err
	}
	var fileSync *fileclipboard.Synchronizer
	if cfg.Files.Enabled {
		backend, err := platform.NewFileClipboard()
		if err != nil {
			return nil, nil, nil, err
		}
		fileSync, err = fileclipboard.NewSynchronizer(backend, func(ctx context.Context, transferID string, paths []string) error {
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
