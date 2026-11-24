package main

import (
	"context"
	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/pointer"
	"testing"
	"time"
)

type countingGeometry struct {
	desktopTestPointer
	calls, version int
}

type topologyGeometry struct {
	desktopTestPointer
	topology pointer.Topology
}

func (p *topologyGeometry) Topology() (pointer.Topology, error) { return p.topology, nil }

type landingInjector struct {
	desktopTestInjector
	point pointer.Point
}

func (i *landingInjector) Inject(event common.Event) error {
	i.point = pointer.Point{X: int(event.X), Y: int(event.Y)}
	return nil
}

func TestTopologyEntryProjectsLandingOntoRealDisplay(t *testing.T) {
	topology, err := pointer.NewTopology([]pointer.Screen{{ID: "primary", Width: 2560, Height: 1440}, {ID: "second", X: -1080, Y: -493, Width: 1080, Height: 1920}})
	if err != nil {
		t.Fatal(err)
	}
	geometry := &topologyGeometry{topology: topology}
	inner := &landingInjector{}
	i := &desktopInjector{state: desktop.New(config.Default("agent", config.RoleAgent), "local"), geometry: geometry, peer: "peer", create: func() (common.Injector, error) { return inner, nil }}
	entry := pointer.Entry{Side: "right", Position: 1, ScreenKey: topology.Bounds.Key()}
	if err := i.controlEdge(context.Background(), "begin", &entry); err != nil {
		t.Fatal(err)
	}
	travel, err := pointer.NewTopologyTravel(topology, entry)
	if err != nil || inner.point != travel.Point || inner.point.Y != 1426 {
		t.Fatalf("目标落点与源端移动模型不一致: %+v %v", inner.point, err)
	}
}

func (p *countingGeometry) Screen() (pointer.Screen, error) {
	p.calls++
	return pointer.Screen{ID: "test", Width: 100 + p.version, Height: 100}, nil
}

func TestInputGeometryChecksAreAmortizedAndDetectLayoutChanges(t *testing.T) {
	p := &countingGeometry{}
	i := &desktopInjector{state: desktop.New(config.Default("agent", config.RoleAgent), "local"), geometry: p, peer: "peer", create: func() (common.Injector, error) { return &desktopTestInjector{}, nil }}
	screen, _ := p.Screen()
	if err := i.controlEdge(context.Background(), "begin", &pointer.Entry{Side: "right", Position: 0.5, ScreenKey: screen.Key()}); err != nil {
		t.Fatal(err)
	}
	before := p.calls
	for n := 1; n <= 100; n++ {
		if err := i.Inject(common.Event{Kind: common.MouseAbsolute, Sequence: uint64(n), X: 20, Y: 30}); err != nil {
			t.Fatal(err)
		}
	}
	if p.calls != before {
		t.Fatal("每个鼠标事件重复查询了显示器")
	}
	p.version++
	i.nextCheck = time.Time{}
	if err := i.Inject(common.Event{Kind: common.MouseAbsolute, Sequence: 101, X: 20, Y: 30}); err == nil {
		t.Fatal("未发现布局变化")
	}
}

func TestStopDuringPreparationDoesNotInstallCapture(t *testing.T) {
	c, ctx := testDesktopCapture(t, func(context.Context, string) error { return nil })
	creating, release := make(chan struct{}), make(chan struct{})
	backend := &desktopTestCapturer{}
	c.create = func() (common.Capturer, error) { close(creating); <-release; return backend, nil }
	started := make(chan error, 1)
	go func() { started <- c.start(ctx, "peer") }()
	<-creating
	stopped := make(chan error, 1)
	go func() { stopped <- c.stop(ctx) }()
	for {
		c.mu.Lock()
		epoch := c.stopEpoch
		c.mu.Unlock()
		if epoch > 0 {
			break
		}
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
			t.Fatal("停止未取消准备状态")
		}
	}
	close(release)
	if err := <-started; err == nil {
		t.Fatal("停止后继续启动")
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if backend.started.Load() {
		t.Fatal("停止后安装了捕获")
	}
}
