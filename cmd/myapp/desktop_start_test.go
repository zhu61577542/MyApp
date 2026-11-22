package main

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"myapp/internal/app"
	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/device"
	common "myapp/internal/input"
)

type desktopTestCapturer struct{ started atomic.Bool }

func (c *desktopTestCapturer) Capture(ctx context.Context, handler func(common.Event) error) error {
	return c.CaptureWithReady(ctx, handler, nil)
}
func (c *desktopTestCapturer) CaptureWithReady(ctx context.Context, _ func(common.Event) error, ready func()) error {
	c.started.Store(true)
	if ready != nil {
		ready()
	}
	<-ctx.Done()
	return ctx.Err()
}
func (c *desktopTestCapturer) Close() error { return nil }

func TestRemoteStopDuringStartPreventsCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	controller, _ := app.NewSplitLink(nil, nil)
	agent, _ := app.NewSplitLink(&desktopTestInjector{}, nil)
	controller.EnableDesktop(app.Capabilities{}, nil, nil)
	agent.EnableDesktop(app.Capabilities{Control: true}, nil, func(context.Context, string) error { return nil })
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
	if err := hub.AddPeer(device.Device{ID: "peer", Name: "peer", Role: config.RoleAgent, Online: true}, controller); err != nil {
		t.Fatal(err)
	}
	state := desktop.New(config.Default("local", config.RoleController), "local")
	c := newDesktopCapture(ctx, hub, []*app.Link{controller}, []config.Peer{{ID: "peer"}}, state)
	creating, release := make(chan struct{}), make(chan struct{})
	backend := &desktopTestCapturer{}
	c.create = func() (common.Capturer, error) { close(creating); <-release; return backend, nil }
	done := make(chan error, 1)
	go func() { done <- c.start(ctx, "peer") }()
	select {
	case <-creating:
	case <-ctx.Done():
		t.Fatal("未进入捕获初始化")
	}
	c.remoteStopped("peer")
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("远端已停止仍报告开始控制")
		}
	case <-ctx.Done():
		t.Fatal("取消初始化超时")
	}
	if backend.started.Load() || state.Snapshot().Target != "" {
		t.Fatal("远端停止后仍安装了本机捕获")
	}
}
