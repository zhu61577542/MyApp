package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"myapp/internal/app"
	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/device"
	common "myapp/internal/input"
	"myapp/internal/pointer"
)

type desktopTestPointer struct {
	hideErr    error
	restoreErr error
	restored   chan struct{}
	warped     chan pointer.Point
}

func (p *desktopTestPointer) Screen() (pointer.Screen, error) {
	return pointer.Screen{ID: "test", Width: 100, Height: 100}, nil
}
func (p *desktopTestPointer) Position() (pointer.Point, error) {
	return pointer.Point{X: 30, Y: 40}, nil
}
func (p *desktopTestPointer) Warp(point pointer.Point) error { p.warped <- point; return nil }
func (p *desktopTestPointer) Hide(context.Context) (func() error, error) {
	if p.hideErr != nil {
		return nil, p.hideErr
	}
	return func() error { close(p.restored); return p.restoreErr }, nil
}

func testDesktopCapture(t *testing.T, end func(context.Context, string) error) (*desktopCapture, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	controller, _ := app.NewSplitLink(nil, nil)
	agent, _ := app.NewSplitLink(&desktopTestInjector{}, nil)
	controller.EnableDesktop(app.Capabilities{}, nil, nil)
	agent.EnableDesktop(app.Capabilities{Control: true}, nil, end)
	a, b := net.Pipe()
	x, y := net.Pipe()
	ended := make(chan error, 2)
	go func() { ended <- controller.RunChannels(ctx, a, x) }()
	go func() { ended <- agent.RunChannels(ctx, b, y) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case <-ended:
			case <-time.After(time.Second):
				t.Error("测试连接未结束")
			}
		}
	})
	hub, err := app.NewHub(device.Device{ID: "local", Name: "local", Role: config.RoleController}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err = hub.AddPeer(device.Device{ID: "peer", Name: "peer", Role: config.RoleAgent, Online: true}, controller); err != nil {
		t.Fatal(err)
	}
	c := newDesktopCapture(ctx, hub, []*app.Link{controller}, []config.Peer{{ID: "peer"}}, desktop.New(config.Default("local", config.RoleController), "local"))
	c.geometry, c.geometryErr = nil, nil
	return c, ctx
}

func TestCursorRestoresBeforeRemoteAcknowledgement(t *testing.T) {
	ack := make(chan struct{})
	c, ctx := testDesktopCapture(t, func(ctx context.Context, action string) error {
		if action == "end" {
			select {
			case <-ack:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	p := &desktopTestPointer{restored: make(chan struct{}), warped: make(chan pointer.Point, 1), restoreErr: errors.New("restore-test-error")}
	c.geometry = p
	c.create = func() (common.Capturer, error) { return &desktopTestCapturer{}, nil }
	if err := c.start(ctx, "peer"); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- c.stop(ctx) }()
	select {
	case <-p.restored:
	case <-ctx.Done():
		t.Fatal("未及时恢复本机光标")
	}
	select {
	case point := <-p.warped:
		if point != (pointer.Point{X: 30, Y: 40}) {
			t.Fatal(point)
		}
	default:
		t.Fatal("未先恢复本机位置")
	}
	select {
	case <-stopped:
		t.Fatal("远端尚未确认就结束清理")
	default:
	}
	close(ack)
	if err := <-stopped; err == nil || !strings.Contains(err.Error(), "restore-test-error") {
		t.Fatalf("恢复错误被取消状态吞掉: %v", err)
	}
	if err := c.start(ctx, "peer"); err == nil || !strings.Contains(err.Error(), "上次光标恢复未确认") {
		t.Fatalf("恢复未确认仍允许新捕获: %v", err)
	}
}

func TestCursorHideFailureDoesNotCapture(t *testing.T) {
	c, ctx := testDesktopCapture(t, func(context.Context, string) error { return nil })
	backend := &desktopTestCapturer{}
	c.geometry = &desktopTestPointer{hideErr: errors.New("hide-test-error")}
	c.create = func() (common.Capturer, error) { return backend, nil }
	if err := c.start(ctx, "peer"); err == nil || !strings.Contains(err.Error(), "hide-test-error") {
		t.Fatal(err)
	}
	if backend.started.Load() || c.state.Snapshot().Target != "" {
		t.Fatal("隐藏失败后仍开始捕获")
	}
}

func TestQueuedEdgeStartHonorsDisabledState(t *testing.T) {
	c, ctx := testDesktopCapture(t, func(context.Context, string) error { return nil })
	c.state.Update(func(s *desktop.Snapshot) { s.EdgeEnabled = true })
	c.op.Lock()
	done := make(chan error, 1)
	go func() { done <- c.startAt(ctx, "peer", &pointer.Entry{Side: "right", Position: 0.5, ScreenKey: "old"}) }()
	c.state.Update(func(s *desktop.Snapshot) { s.EdgeEnabled = false })
	c.op.Unlock()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "跨屏切换已关闭") {
		t.Fatalf("已排队的跨屏仍启动: %v", err)
	}
}

type desktopFailCapturer struct{ desktopTestCapturer }

func (*desktopFailCapturer) CaptureWithReady(context.Context, func(common.Event) error, func()) error {
	return errors.New("startup-test-error")
}

func TestCaptureCanRestartAfterStartupFailure(t *testing.T) {
	c, ctx := testDesktopCapture(t, func(context.Context, string) error { return nil })
	c.create = func() (common.Capturer, error) { return &desktopFailCapturer{}, nil }
	if err := c.start(ctx, "peer"); err == nil {
		t.Fatal("未报告启动错误")
	}
	c.create = func() (common.Capturer, error) { return &desktopTestCapturer{}, nil }
	if err := c.start(ctx, "peer"); err != nil {
		t.Fatalf("历史错误阻止重试: %v", err)
	}
	if err := c.stop(ctx); err != nil {
		t.Fatal(err)
	}
}
