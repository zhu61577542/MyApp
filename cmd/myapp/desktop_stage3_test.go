package main

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"myapp/internal/app"
	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
)

type stage3Pointer struct {
	peerNativePointer
	displays []pointer.Screen
	point    atomic.Pointer[pointer.Point]
	observed atomic.Pointer[pointer.Point]
}

func (p *stage3Pointer) Screen() (pointer.Screen, error)     { return p.displays[0], nil }
func (p *stage3Pointer) Topology() (pointer.Topology, error) { return pointer.NewTopology(p.displays) }
func (p *stage3Pointer) Position() (pointer.Point, error) {
	point := p.point.Load()
	p.observed.Store(point)
	return *point, nil
}
func (p *stage3Pointer) Warp(point pointer.Point) error {
	p.point.Store(&point)
	p.warped.Add(1)
	return nil
}

type stage3Pair struct {
	ctx         context.Context
	links       [2]*app.Link
	states      [2]*desktop.Session
	geometry    [2]*stage3Pointer
	activity    [2]*stage3Observer
	capture     [2]*peerNativeCapture
	injectors   [2]*stage3RecordingInjector
	controllers [2]*desktopCapture
}

func newStage3Pair(t *testing.T, windowsSide string) *stage3Pair {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	p := &stage3Pair{ctx: ctx}
	ids := [2]string{"a-mac", "b-windows"}
	for i := range p.links {
		cfg := config.Default(ids[i], []config.Role{config.RoleController, config.RoleAgent}[i])
		cfg.Peers = []config.Peer{{ID: ids[1-i], Position: windowsSide}}
		p.states[i] = desktop.New(cfg, ids[i])
		localCtx := withDesktopPeer(desktop.WithSession(ctx, p.states[i]), p.states[i])
		p.geometry[i] = &stage3Pointer{displays: []pointer.Screen{{ID: "mac", Width: 1000, Height: 800}}}
		if i == 1 {
			p.geometry[i].displays = []pointer.Screen{{ID: "primary", Width: 2560, Height: 1440}, {ID: "second", X: -1080, Y: -493, Width: 1080, Height: 1920}}
		}
		p.geometry[i].point.Store(&pointer.Point{X: 100, Y: 500})
		p.activity[i] = &stage3Observer{events: make(chan common.Event, 32), failure: make(chan error, 1)}
		p.capture[i] = &peerNativeCapture{events: make(chan common.Event, 32)}
		p.injectors[i] = &stage3RecordingInjector{}
		runtime := desktopPeerFrom(localCtx)
		runtime.driver.geometry = func() (pointer.Backend, error) { return p.geometry[i], nil }
		runtime.driver.createCapture = func() (common.Capturer, error) { return p.capture[i], nil }
		runtime.driver.createInjector = func() (common.Injector, error) { return p.injectors[i], nil }
		p.links[i], _ = app.NewSplitLink(nil, nil)
		if err := enableDesktopLink(localCtx, p.links[i], cfg, desktop.Peer{ID: ids[1-i]}, nil); err != nil {
			t.Fatal(err)
		}
		p.links[i].EnableScreen(p.geometry[i].Screen, nil)
		p.links[i].EnableTopology(p.geometry[i].Topology)
		p.controllers[i] = newDesktopCapture(localCtx, nil, []*app.Link{p.links[i]}, cfg.Peers, p.states[i])
		p.controllers[i].geometry = p.geometry[i]
		p.controllers[i].geometryErr = nil
		p.controllers[i].activity.create = func() (common.PhysicalObserver, error) { return p.activity[i], nil }
	}
	a, b := net.Pipe()
	x, y := net.Pipe()
	done := make(chan error, 4)
	go func() { done <- p.links[0].RunChannels(ctx, a, x) }()
	go func() { done <- p.links[1].RunChannels(ctx, b, y) }()
	for i := range p.controllers {
		go func() { done <- p.controllers[i].runEdges(ctx) }()
	}
	t.Cleanup(func() {
		cancel()
		for range 4 {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("第三阶段会话没有退出")
			}
		}
	})
	nativeWait(t, func() bool { _, a := p.links[0].PeerPosition(); _, b := p.links[1].PeerPosition(); return a && b })
	return p
}

func (p *stage3Pair) move(t *testing.T, index int, point pointer.Point) {
	t.Helper()
	p.geometry[index].point.Store(&point)
	p.activity[index].events <- common.Event{Kind: common.MouseAbsolute, X: int32(point.X), Y: int32(point.Y)}
	nativeWait(t, func() bool { return p.geometry[index].observed.Load() == &point })
}

func TestStage3AutomaticCrossingBothPhysicalSourcesAndTakeover(t *testing.T) {
	for _, side := range []string{"left", "right"} {
		t.Run(side, func(t *testing.T) {
			p := newStage3Pair(t, side)
			p.move(t, 1, pointer.Point{X: 100, Y: 500})
			edge := pointer.Point{X: 2559, Y: 500}
			if side == "right" {
				p.move(t, 1, pointer.Point{X: -2, Y: 500})
				if p.geometry[1].hidden.Load() != 0 {
					t.Fatal("Windows内部接缝触发跨机")
				}
				edge.X = -1080
			}
			p.move(t, 1, edge)
			nativeWait(t, func() bool {
				return p.geometry[1].hidden.Load() == 1 && p.states[0].Sharing().ControlPhase == string(ownership.Active)
			})
			p.checkKeyboard(t, 1)
			p.activity[0].events <- common.Event{Kind: common.KeyDown, Code: 4}
			nativeWait(t, func() bool {
				return p.geometry[1].restored.Load() == 1 && p.states[0].Sharing().ControlPhase == string(ownership.Local) && p.states[1].Sharing().ControlPhase == string(ownership.Local)
			})
			if p.links[0].CurrentPeerControl().Err() != nil || p.links[1].CurrentPeerControl().Err() != nil {
				t.Fatal("实体接管中断连接")
			}
			p.move(t, 0, pointer.Point{X: 500, Y: 400})
			macEdge := pointer.Point{X: 0, Y: 400}
			if side == "right" {
				macEdge.X = 999
			}
			p.move(t, 0, macEdge)
			nativeWait(t, func() bool {
				return p.geometry[0].hidden.Load() == 1 && p.states[1].Sharing().ControlPhase == string(ownership.Active)
			})
			p.checkKeyboard(t, 0)
			p.activity[1].events <- common.Event{Kind: common.MouseAbsolute, X: 100, Y: 500}
			nativeWait(t, func() bool {
				return p.geometry[0].restored.Load() == 1 && p.states[0].Sharing().ControlPhase == string(ownership.Local)
			})
			if p.links[0].CurrentPeerControl().Err() != nil {
				t.Fatal("反向实体接管中断连接")
			}
		})
	}
}

func TestStage3ReceivedCursorCannotTriggerAutomaticBounce(t *testing.T) {
	p := newStage3Pair(t, "left")
	p.move(t, 0, pointer.Point{X: 500, Y: 400})
	p.move(t, 0, pointer.Point{X: 0, Y: 400})
	nativeWait(t, func() bool { return p.states[1].Sharing().ControlPhase == string(ownership.Active) })
	// 模拟注入改变目标位置，没有实体移动通知。
	p.geometry[1].point.Store(&pointer.Point{X: 2559, Y: 100})
	time.Sleep(25 * time.Millisecond)
	if p.geometry[1].hidden.Load() != 0 {
		t.Fatal("被控状态启动了第二个来源")
	}
	if err := p.links[0].CurrentPeerControl().Stop(ownership.Returned); err != nil {
		t.Fatal(err)
	}
	nativeWait(t, func() bool { return p.states[1].Sharing().ControlPhase == string(ownership.Local) })
	time.Sleep(25 * time.Millisecond)
	if p.geometry[1].hidden.Load() != 0 {
		t.Fatal("恢复后被注入的边缘位置自动反弹")
	}
}

func TestStage3FinalPhysicalPositionIsSampledAfterHookReturns(t *testing.T) {
	p := newStage3Pair(t, "left")
	p.move(t, 1, pointer.Point{X: 100, Y: 500})
	stale := pointer.Point{X: 101, Y: 500}
	p.geometry[1].point.Store(&stale)
	// Hook先通知，系统光标在回调返回后才到达最终位置。
	p.controllers[1].activity.observe(common.Event{Kind: common.MouseAbsolute, X: 2559, Y: 500})
	nativeWait(t, func() bool { return p.geometry[1].observed.Load() == &stale })
	p.geometry[1].point.Store(&pointer.Point{X: 2559, Y: 500})
	nativeWait(t, func() bool { return p.geometry[1].hidden.Load() == 1 })
}

func TestStage3WindowsReturnLandsOnRealSourceDisplay(t *testing.T) {
	for _, side := range []string{"left", "right"} {
		t.Run(side, func(t *testing.T) {
			p := newStage3Pair(t, side)
			center, edge := pointer.Point{X: 100, Y: 1400}, pointer.Point{X: 2559, Y: 1400}
			delta := -10
			if side == "right" {
				center, edge = pointer.Point{X: -500, Y: -400}, pointer.Point{X: -1080, Y: -400}
				delta = 10
			}
			p.move(t, 1, center)
			p.move(t, 1, edge)
			nativeWait(t, func() bool {
				return p.geometry[1].hidden.Load() == 1 && p.states[0].Sharing().ControlPhase == string(ownership.Active)
			})
			p.capture[1].events <- common.Event{Kind: common.MouseRelative, X: int32(delta), Sequence: 1}
			nativeWait(t, func() bool {
				return p.geometry[1].restored.Load() == 1 && p.states[1].Sharing().ControlPhase == string(ownership.Local)
			})
			point, _ := p.geometry[1].Position()
			inside := false
			for _, display := range p.geometry[1].displays {
				inside = inside || display.Contains(point)
			}
			if !inside {
				t.Fatal("返程落到了没有显示器的空白区域", point)
			}
			if side == "right" && point.X >= 0 {
				t.Fatal("扩展屏发起的跨屏没有返回正确外边缘")
			}
		})
	}
}

type stage3RecordingInjector struct {
	peerNativeInjector
	mu       sync.Mutex
	keyboard []common.Event
}

func (i *stage3RecordingInjector) Inject(e common.Event) error {
	if keyboardEvent(e) {
		i.mu.Lock()
		i.keyboard = append(i.keyboard, e)
		i.mu.Unlock()
	}
	return i.peerNativeInjector.Inject(e)
}
func (i *stage3RecordingInjector) keyboardEvents() []common.Event {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]common.Event(nil), i.keyboard...)
}
func (p *stage3Pair) checkKeyboard(t *testing.T, source int) {
	t.Helper()
	target := p.injectors[1-source]
	before := len(target.keyboardEvents())
	expected := []common.Event{
		{Kind: common.KeyDown, Code: 4}, {Kind: common.KeyUp, Code: 4},
		{Kind: common.KeyDown, Code: 30}, {Kind: common.KeyUp, Code: 30},
		{Kind: common.KeyDown, Code: 225}, {Kind: common.KeyDown, Code: 5},
		{Kind: common.KeyUp, Code: 5}, {Kind: common.KeyUp, Code: 225},
	}
	for n, e := range expected {
		e.Sequence = uint64(n + 1)
		p.capture[source].events <- e
	}
	nativeWait(t, func() bool { return len(target.keyboardEvents()) >= before+len(expected) })
	got := target.keyboardEvents()[before:]
	if len(got) != len(expected) {
		t.Fatal("键盘事件数量错误", len(got))
	}
	for n, e := range got {
		if e.Kind != expected[n].Kind || e.Code != expected[n].Code {
			t.Fatal("按下释放顺序或键映射不一致", n)
		}
	}
}

func TestStage3PhysicalTakeoverContinuesDirectlyAcrossLandingEdge(t *testing.T) {
	p := newStage3Pair(t, "left")
	p.move(t, 0, pointer.Point{X: 500, Y: 400})
	p.move(t, 0, pointer.Point{X: 0, Y: 400})
	nativeWait(t, func() bool { return p.states[1].Sharing().ControlPhase == string(ownership.Active) })
	// 入口距外边缘2像素；仅一条本机朝外移动，不先回到屏幕中央。
	p.geometry[1].point.Store(&pointer.Point{X: 2559, Y: 500})
	p.controllers[1].activity.observe(common.Event{Kind: common.MouseRelative, X: 2})
	nativeWait(t, func() bool {
		s := p.states[1].Sharing()
		return s.ControlPhase == string(ownership.Active) && s.ControlSource == "b-windows" && p.geometry[1].hidden.Load() == 1
	})
	p.checkKeyboard(t, 1)
	if p.links[0].CurrentPeerControl().Err() != nil || p.links[1].CurrentPeerControl().Err() != nil {
		t.Fatal("接续破坏连接")
	}
}

func TestStage3ReenableDoesNotReplayDisabledMovement(t *testing.T) {
	p := newStage3Pair(t, "left")
	c := p.controllers[1]
	unbind := bindDesktop(c.ctx, nil, nil, c, nil)
	defer unbind()
	for _, action := range []string{"edge", "pause"} {
		disabled := action == "pause"
		if err := p.states[1].Apply(p.ctx, desktop.Command{Version: desktop.Version, ID: 1, Action: action, Enabled: disabled}); err != nil {
			t.Fatal(err)
		}
		p.geometry[1].point.Store(&pointer.Point{X: 2559, Y: 500})
		c.activity.observe(common.Event{Kind: common.MouseRelative, X: 2})
		if err := p.states[1].Apply(p.ctx, desktop.Command{Version: desktop.Version, ID: 2, Action: action, Enabled: !disabled}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
		if p.geometry[1].hidden.Load() != 0 {
			t.Fatal("重新启用重放了停用期间的移动", action)
		}
	}
	c.activity.observe(common.Event{Kind: common.MouseRelative, X: 1})
	nativeWait(t, func() bool { return p.geometry[1].hidden.Load() == 1 })
}
