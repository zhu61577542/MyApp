package main

import (
	"context"
	"crypto/rand"
	"errors"
	"runtime"
	"time"

	"myapp/internal/app"
	"myapp/internal/clipboard"
	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/fileclipboard"
	"myapp/internal/pointer"
	"myapp/internal/transfer"
	"myapp/internal/trust"
)

func peerView(store trust.Store, id, address string) desktop.Peer {
	peer := desktop.Peer{ID: id, Address: address, Status: "connected", Name: id}
	if record, err := store.Get(id); err == nil {
		peer.Name = record.DeviceName
	}
	return peer
}

func enableDesktopLink(ctx context.Context, link *app.Link, cfg config.Config, peer desktop.Peer, gate *desktopInjector) {
	state := desktop.From(ctx)
	if state == nil {
		return
	}
	state.Peer(peer)
	state.SetSender(peer.ID, func(paths []string) error {
		if err := ctx.Err(); err != nil {
			return errors.New("连接已结束，请重新连接后再发送")
		}
		if current := state.Snapshot(); current.Paused || !current.Files {
			return errors.New("请先恢复文件共享")
		}
		sources := make([]transfer.Source, len(paths))
		for j, path := range paths {
			sources[j] = transfer.Source{Path: path}
		}
		return link.QueueFiles(ctx, rand.Text(), sources)
	})
	link.ObserveTransfers(func(event app.TransferEvent, cancel func(context.Context) error) {
		id := peer.ID + ":" + event.Direction + ":" + event.ID
		state.Task(desktop.Task{ID: id, PeerID: peer.ID, Direction: event.Direction, Name: event.Name, State: event.State, Bytes: event.Bytes, Total: event.Total, Path: event.Path, Error: event.Error}, func(commandCtx context.Context, command desktop.Command) error {
			if command.Action == "retry_task" {
				if event.Direction != "send" {
					return errors.New("请在源电脑重新复制文件")
				}
				return state.RetryFiles(peer.ID, event.Sources)
			}
			return cancel(commandCtx)
		})
	}, func() bool { current := state.Snapshot(); return current.Files && !current.Paused })
	var control func(context.Context, string) error
	if gate != nil {
		control = gate.control
		gate.onBlocked = func() error { return link.NotifyControlEnded(ctx) }
	}
	link.EnableDesktop(app.Capabilities{Name: cfg.DeviceName, OS: runtime.GOOS, SoftwareVersion: version, Control: gate != nil, FileResults: true}, func(caps app.Capabilities) {
		state.UpdatePeer(peer.ID, func(update *desktop.Peer) {
			update.OS = caps.OS
			update.Version = caps.SoftwareVersion
			update.Control = caps.Control
			update.FileResults = caps.FileResults
			if caps.Name != "" {
				update.Name = caps.Name
			}
		})
	}, control)
	geometry, geometryErr := pointer.New()
	if gate != nil {
		gate.geometry = geometry
		link.SetEdgeControlHandler(gate.controlEdge)
	}
	link.EnableScreen(func() (pointer.Screen, error) {
		if geometryErr != nil {
			return pointer.Screen{}, geometryErr
		}
		return geometry.Screen()
	}, func(info app.ScreenInfo) {
		state.UpdatePeer(peer.ID, func(update *desktop.Peer) {
			update.Edge = info.Screen.Valid()
			update.ScreenWidth = info.Screen.Width
			update.ScreenHeight = info.Screen.Height
		})
	})
}

func bindDesktop(ctx context.Context, text *clipboard.Synchronizer, files *fileclipboard.Synchronizer, capture *desktopCapture, gate *desktopInjector) func() {
	state := desktop.From(ctx)
	if state == nil {
		return func() {}
	}
	apply := func(paused, textEnabled, filesEnabled, accept bool) error {
		if gate != nil {
			if err := gate.block(paused || !accept); err != nil {
				return err
			}
		}
		if text != nil {
			text.SetPaused(paused || !textEnabled)
		}
		if files != nil {
			files.SetPaused(paused || !filesEnabled)
		}
		return nil
	}
	return state.Bind(func(commandCtx context.Context, command desktop.Command) error {
		current := state.Snapshot()
		switch command.Action {
		case "start_control":
			if capture == nil {
				return errors.New("这台电脑当前是子电脑")
			}
			return capture.start(commandCtx, command.Target)
		case "stop_control":
			if capture != nil {
				state.Update(func(s *desktop.Snapshot) { s.EdgeEnabled = false })
				return capture.stop(commandCtx)
			}
			if gate != nil {
				return gate.block(true)
			}
			return nil
		case "pause":
			if command.Enabled {
				state.Update(func(s *desktop.Snapshot) { s.Paused = true })
			}
			if command.Enabled && capture != nil {
				if err := capture.stop(commandCtx); err != nil {
					return err
				}
			}
			if err := apply(command.Enabled, current.Text, current.Files, current.Accepting); err != nil {
				return err
			}
			state.Update(func(s *desktop.Snapshot) {
				s.Paused = command.Enabled
				if !command.Enabled && capture != nil {
					s.EdgeEnabled = true
				}
			})
		case "edge":
			if capture == nil {
				return errors.New("只有主电脑可以启用跨屏切换")
			}
			if command.Enabled && capture.geometry == nil {
				return errors.New("当前系统无法读取主屏幕或管理光标，不能启用跨屏切换")
			}
			if command.Enabled && current.Paused {
				return errors.New("请先恢复共享")
			}
			if !command.Enabled {
				state.Update(func(s *desktop.Snapshot) { s.EdgeEnabled = false })
				if err := capture.stop(commandCtx); err != nil {
					return err
				}
			}
			state.Update(func(s *desktop.Snapshot) { s.EdgeEnabled = command.Enabled })
		case "layout":
			if capture == nil {
				return errors.New("只有主电脑可以设置位置")
			}
			state.Update(func(s *desktop.Snapshot) { s.EdgeEnabled = false })
			if err := capture.stop(commandCtx); err != nil {
				return err
			}
			if err := capture.updateLayout(command.Layout); err != nil {
				return err
			}
			state.Update(func(s *desktop.Snapshot) { s.EdgeEnabled = command.Enabled && !s.Paused })
			return nil
		case "text":
			if text == nil {
				return errors.New("文字剪贴板后端不可用，请查看诊断")
			}
			text.SetPaused(current.Paused || !command.Enabled)
			state.Update(func(s *desktop.Snapshot) { s.Text = command.Enabled })
		case "files":
			if files == nil {
				return errors.New("文件剪贴板后端不可用，请查看诊断")
			}
			files.SetPaused(current.Paused || !command.Enabled)
			state.Update(func(s *desktop.Snapshot) { s.Files = command.Enabled })
		case "accept":
			if gate == nil {
				return errors.New("主电脑不接受来自子电脑的键鼠控制")
			}
			if err := gate.block(current.Paused || !command.Enabled); err != nil {
				return err
			}
			state.Update(func(s *desktop.Snapshot) { s.Accepting = command.Enabled })
		default:
			return errors.New("当前运行时不支持该操作")
		}
		return nil
	}, func() {
		current := state.Snapshot()
		_ = apply(current.Paused, current.Text, current.Files, current.Accepting)
	})
}

func finishDesktopCapture(capture *desktopCapture) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = capture.stop(ctx)
}
