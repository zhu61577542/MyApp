package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
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
	position   func() pointer.Point
	hideErr    error
	restoreErr error
	restored   chan struct{}
	warped     chan pointer.Point
}

func (p *desktopTestPointer) Screen() (pointer.Screen, error) {
	return pointer.Screen{ID: "test", Width: 100, Height: 100}, nil
}
func (p *desktopTestPointer) Position() (pointer.Point, error) {
	if p.position != nil {
		return p.position(), nil
	}
	return pointer.Point{X: 30, Y: 40}, nil
}
func (p *desktopTestPointer) Warp(point pointer.Point) error { p.warped <- point; return nil }
func (p *desktopTestPointer) Hide(context.Context) (func() error, error) {
	if p.hideErr != nil {
		return nil, p.hideErr
	}
	return func() error { close(p.restored); return p.restoreErr }, nil
}

func testDesktopCapture(t *testing.T, end func(context.Context, string) error, screens ...bool) (*desktopCapture, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	controller, _ := app.NewSplitLink(nil, nil)
	agent, _ := app.NewSplitLink(&desktopTestInjector{}, nil)
	controller.EnableDesktop(app.Capabilities{}, nil, nil)
	agent.EnableDesktop(app.Capabilities{Control: true}, nil, end)
	if len(screens) > 0 && screens[0] {
		controller.EnableScreen(nil, nil)
		agent.EnableScreen(func() (pointer.Screen, error) { return pointer.Screen{ID: "peer", Width: 100, Height: 100}, nil }, nil)
		agent.SetEdgeControlHandler(func(context.Context, string, *pointer.Entry) error { return nil })
	}
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

func TestAutomaticEdgeCrossingNeedsNoControlCommand(t *testing.T) {
	c, ctx := testDesktopCapture(t, func(context.Context, string) error { return nil }, true)
	var x atomic.Int32
	x.Store(30)
	inside := make(chan struct{})
	var observed atomic.Bool
	c.geometry = &desktopTestPointer{restored: make(chan struct{}), warped: make(chan pointer.Point, 1), position: func() pointer.Point {
		if x.Load() == 30 && observed.CompareAndSwap(false, true) {
			close(inside)
		}
		return pointer.Point{X: int(x.Load()), Y: 40}
	}}
	backend := &desktopTestCapturer{}
	c.create = func() (common.Capturer, error) { return backend, nil }
	c.edgeDelay = 0
	edgeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ended := make(chan error, 1)
	go func() { ended <- c.runEdges(edgeCtx) }()
	select {
	case <-inside:
	case <-ctx.Done():
		t.Fatal("未自动监测本机指针")
	}
	for !c.links[0].RemoteScreen().Screen.Valid() {
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal("没有收到屏幕能力")
		}
	}
	x.Store(99)
	for !backend.started.Load() {
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal("移过边缘后仍要求控制按钮")
		}
	}
	if err := c.stop(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-ended
}

func TestLayoutAndPausePreserveAutomaticCrossingIntent(t *testing.T) {
	c, ctx := testDesktopCapture(t, func(context.Context, string) error { return nil })
	unbind := bindDesktop(desktop.WithSession(ctx, c.state), nil, nil, c, nil)
	defer unbind()
	commands := []desktop.Command{
		{Action: "pause", Enabled: true},
		{Action: "layout", Enabled: true, Layout: map[string]string{"peer": "left"}},
		{Action: "pause", Enabled: false},
		{Action: "layout", Enabled: true, Layout: map[string]string{"peer": "right"}},
		{Action: "stop_control"},
		{Action: "layout", Enabled: false, Layout: map[string]string{"peer": "left"}},
	}
	for index, command := range commands {
		command.Version = desktop.Version
		command.ID = uint64(index + 1)
		if err := c.state.Apply(ctx, command); err != nil {
			t.Fatal(err)
		}
		snapshot := c.state.Sharing()
		if index == 1 && (!snapshot.Paused || snapshot.EdgeEnabled) {
			t.Fatal("位置更新覆盖了主动暂停")
		}
		if (index == 2 || index == 3) && !snapshot.EdgeEnabled {
			t.Fatal("恢复共享或保存位置后还要求开启跨屏")
		}
		if index == 5 && snapshot.EdgeEnabled {
			t.Fatal("位置更新覆盖了紧急停止意图")
		}
	}
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

func TestCursorHideFailureStopsPreparedCapture(t *testing.T) {
	c, ctx := testDesktopCapture(t, func(context.Context, string) error { return nil })
	backend := &desktopTestCapturer{}
	c.geometry = &desktopTestPointer{hideErr: errors.New("hide-test-error")}
	c.create = func() (common.Capturer, error) { return backend, nil }
	if err := c.start(ctx, "peer"); err == nil || !strings.Contains(err.Error(), "hide-test-error") {
		t.Fatal(err)
	}
	if !backend.started.Load() || c.state.Snapshot().Target != "" || c.run != nil {
		t.Fatal("隐藏失败后没有结束预备捕获")
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

func TestNativeCaptureIsReadyBeforeRequestingTarget(t *testing.T) {
	backend := &desktopTestCapturer{}
	c, ctx := testDesktopCapture(t, func(_ context.Context, action string) error {
		if action == "begin" && !backend.started.Load() {
			return errors.New("等待远端确认时本机尚未捕获输入")
		}
		return nil
	})
	c.create = func() (common.Capturer, error) { return backend, nil }
	if err := c.start(ctx, "peer"); err != nil {
		t.Fatal(err)
	}
	if err := c.stop(ctx); err != nil {
		t.Fatal(err)
	}
}
