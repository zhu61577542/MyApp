package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"

	"myapp/internal/app"
	"myapp/internal/clipboard"
	"myapp/internal/config"
	"myapp/internal/device"
	"myapp/internal/fileclipboard"
	"myapp/internal/identity"
	common "myapp/internal/input"
	"myapp/internal/platform"
	"myapp/internal/transport"
	"myapp/internal/trust"
)

var errEmergencyStop = errors.New("用户结束控制")

type retryableControllerError struct{ err error }

func (e retryableControllerError) Error() string { return e.err.Error() }
func (e retryableControllerError) Unwrap() error { return e.err }

func runController(ctx context.Context, cfg config.Config, local identity.Identity, store trust.Store, stdout io.Writer) error {
	backoff, err := transport.NewBackoff(500*time.Millisecond, 10*time.Second)
	if err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		err := runControllerOnce(ctx, cfg, local, store, stdout)
		if err == nil || errors.Is(err, errEmergencyStop) || ctx.Err() != nil {
			return nil
		}
		var retryable retryableControllerError
		if !errors.As(err, &retryable) {
			return err
		}
		fmt.Fprintf(stdout, "连接中断，第 %d 次重连：%v\n", attempt, retryable.err)
		if err := backoff.Wait(ctx); err != nil {
			return nil
		}
	}
}

func runControllerOnce(ctx context.Context, cfg config.Config, local identity.Identity, store trust.Store, stdout io.Writer) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)
	hub, err := app.NewHub(device.Device{ID: local.DeviceID, Name: cfg.DeviceName, Role: cfg.Role, Online: true}, cfg.MaxDevices)
	if err != nil {
		return err
	}
	filesBackend, err := platform.NewFileClipboard()
	if err != nil {
		return err
	}
	var textSync *clipboard.Synchronizer
	var fileSync *fileclipboard.Synchronizer
	var workers []func(context.Context) error
	if cfg.Clipboard.TextEnabled {
		backend, err := platform.NewClipboard()
		if err != nil {
			return err
		}
		engine, err := clipboard.NewEngine(local.DeviceID)
		if err != nil {
			return err
		}
		textSync, err = clipboard.NewSynchronizer(engine, clipboard.TextOnlyBackend{Backend: backend, Files: filesBackend}, func(ctx context.Context, entry clipboard.Entry) error {
			return hub.PublishText(ctx, "", entry)
		}, 150*time.Millisecond)
		if err != nil {
			return err
		}
		workers = append(workers, textSync.Run)
	}
	if cfg.Files.Enabled {
		fileSync, err = fileclipboard.NewSynchronizer(filesBackend, func(ctx context.Context, id string, paths []string) error {
			return hub.PublishFiles(ctx, "", id, paths)
		}, 250*time.Millisecond)
		if err != nil {
			return err
		}
		workers = append(workers, fileSync.Run)
	}
	var receiveMu sync.Mutex
	var connections []*tls.Conn
	for index, peer := range cfg.Peers {
		dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: transport.ClientTLS(local, peer.ID, store)}
		raw, err := dialer.DialContext(ctx, "tcp", peer.Address)
		if err != nil {
			return retryableControllerError{fmt.Errorf("连接目标 %d: %w", index+1, err)}
		}
		connection := raw.(*tls.Conn)
		connections = append(connections, connection)
		defer connection.Close()
		hello, err := transport.NewSessionHello(local, cfg.Role, transport.ChannelRealtime, time.Now())
		if err != nil {
			return err
		}
		var remote transport.SessionHello
		if err := withHandshakeDeadline(ctx, connection, 5*time.Second, func() error {
			var err error
			remote, err = transport.ExchangeHello(connection, hello, peer.ID, true, time.Now())
			return err
		}); err != nil {
			return retryableControllerError{err}
		}
		rawBulk, err := dialer.DialContext(ctx, "tcp", peer.Address)
		if err != nil {
			return retryableControllerError{err}
		}
		bulk := rawBulk.(*tls.Conn)
		defer bulk.Close()
		if err := negotiateBulk(ctx, bulk, hello, remote, true); err != nil {
			return retryableControllerError{err}
		}
		link, err := app.NewSplitLink(nil, nil)
		if err != nil {
			return err
		}
		link.SetDisconnectHandler(cancel)
		if textSync != nil {
			link.SetTextReceiver(func(ctx context.Context, entry clipboard.Entry) error {
				receiveMu.Lock()
				defer receiveMu.Unlock()
				if entry.Origin != peer.ID {
					return errors.New("子电脑剪贴板来源与认证身份不一致")
				}
				decision, err := textSync.Apply(ctx, entry)
				if err != nil || decision != clipboard.Accepted {
					return err
				}
				return hub.PublishText(ctx, peer.ID, entry)
			})
		}
		if fileSync != nil {
			cache := filepath.Join(cfg.Files.Cache, fmt.Sprintf("peer-%d", index+1))
			if err := link.EnableTransfers(cache, func(ctx context.Context, paths []string) error {
				if err := fileSync.Publish(ctx, paths); err != nil {
					return err
				}
				return hub.PublishFiles(ctx, peer.ID, rand.Text(), paths)
			}); err != nil {
				return err
			}
		}
		if err := hub.AddPeer(device.Device{ID: peer.ID, Name: peer.ID, Role: config.RoleAgent, Online: true}, link); err != nil {
			return err
		}
		workers = append(workers,
			func(ctx context.Context) error { return link.RunChannels(ctx, connection, bulk) },
			func(ctx context.Context) error { return hub.RunRelay(ctx, peer.ID) })
	}
	if err := hub.Activate(ctx, 0); err != nil {
		return err
	}
	capturer, err := platform.NewCapturer()
	if err != nil {
		return err
	}
	defer capturer.Close()
	for _, connection := range connections {
		if err := withHandshakeDeadline(ctx, connection, 5*time.Second, func() error {
			return transport.NewHeartbeat(0, time.Now()).Encode(connection, 8)
		}); err != nil {
			return retryableControllerError{err}
		}
	}
	emergency := newEmergencyKeys(func() { cancel(errEmergencyStop) })
	workers = append(workers, func(ctx context.Context) error {
		choose := newTargetKeys(len(cfg.Peers), func(index int) error { return hub.Activate(ctx, index) })
		return capturer.Capture(ctx, func(event common.Event) error {
			if emergency(event) {
				return nil
			}
			if consumed, err := choose(event); consumed || err != nil {
				return err
			}
			return hub.SendInput(ctx, event)
		})
	})
	fmt.Fprintf(stdout, "已连接 %d 台子电脑；Ctrl+Alt+1/2 切换目标，Ctrl+Alt+Esc 退出控制\n", len(cfg.Peers))
	err = runSession(ctx, workers...)
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		if errors.Is(cause, errEmergencyStop) {
			return cause
		}
		return retryableControllerError{cause}
	}
	if err == nil || errors.Is(err, context.Canceled) {
		return nil
	}
	return retryableControllerError{err}
}

func newTargetKeys(count int, activate func(int) error) func(common.Event) (bool, error) {
	pressed := make(map[uint16]bool)
	consumed := make(map[uint16]bool)
	return func(event common.Event) (bool, error) {
		if event.Kind == common.KeyUp {
			delete(pressed, event.Code)
			if consumed[event.Code] {
				delete(consumed, event.Code)
				return true, nil
			}
		}
		if event.Kind != common.KeyDown {
			return false, nil
		}
		pressed[event.Code] = true
		if consumed[event.Code] {
			return true, nil
		}
		control, alt := pressed[224] || pressed[228], pressed[226] || pressed[230]
		index := int(event.Code) - 30
		if control && alt && index >= 0 && index < count {
			consumed[event.Code] = true
			return true, activate(index)
		}
		return false, nil
	}
}
