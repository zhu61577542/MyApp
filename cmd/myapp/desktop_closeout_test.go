package main

import (
	"testing"
	"time"

	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
)

func closeoutCross(t *testing.T, p *stage3Pair, source int, expected int64) {
	t.Helper()
	screen, err := pointer.SourceScreen(p.geometry[source])
	if err != nil {
		t.Fatal(err)
	}
	center := pointer.Point{X: screen.X + screen.Width/2, Y: screen.Y + screen.Height/2}
	p.move(t, source, center)
	edge := center
	if source == 0 {
		edge.X = screen.X
	} else {
		edge.X = screen.X + screen.Width - 1
	}
	p.move(t, source, edge)
	nativeWait(t, func() bool {
		return p.geometry[source].hidden.Load() == expected && p.states[source].Sharing().ControlPhase == string(ownership.Active) && p.states[source].Sharing().ControlSource == []string{"a-mac", "b-windows"}[source]
	})
}

func closeoutLocal(t *testing.T, p *stage3Pair, source int, expected int64) {
	t.Helper()
	nativeWait(t, func() bool {
		return p.geometry[source].restored.Load() == expected && p.states[0].Sharing().ControlPhase == string(ownership.Local) && p.states[1].Sharing().ControlPhase == string(ownership.Local)
	})
	for _, link := range p.links {
		if err := link.CurrentPeerControl().Err(); err != nil {
			t.Fatal("恢复本机破坏了连接", err)
		}
	}
}

func TestStage3CloseoutEmergencyAndPauseRestoreThenRequireFreshMotion(t *testing.T) {
	for _, source := range []int{0, 1} {
		for _, mode := range []string{"emergency", "pause"} {
			t.Run([]string{"mac", "windows"}[source]+"/"+mode, func(t *testing.T) {
				p := newStage3Pair(t, "left")
				c := p.controllers[source]
				unbind := bindDesktop(c.ctx, nil, nil, c, nil)
				defer unbind()
				closeoutCross(t, p, source, 1)
				p.checkKeyboard(t, source)
				if mode == "emergency" {
					for _, code := range []uint16{224, 226, 41} {
						p.capture[source].events <- common.Event{Kind: common.KeyDown, Code: code}
					}
					nativeWait(t, func() bool { return p.states[source].Sharing().Paused })
				} else if err := p.states[source].Apply(p.ctx, desktop.Command{Version: desktop.Version, ID: 1, Action: "pause", Enabled: true}); err != nil {
					t.Fatal(err)
				}
				closeoutLocal(t, p, source, 1)
				if p.injectors[1-source].releases.Load() == 0 {
					t.Fatal("结束控制没有释放目标按键")
				}
				for _, code := range []uint16{224, 226, 41} {
					p.activity[source].events <- common.Event{Kind: common.KeyUp, Code: code}
				}
				if err := p.states[source].Apply(p.ctx, desktop.Command{Version: desktop.Version, ID: 2, Action: "pause", Enabled: false}); err != nil {
					t.Fatal(err)
				}
				time.Sleep(40 * time.Millisecond)
				if p.geometry[source].hidden.Load() != 1 {
					t.Fatal("恢复共享重放了旧触边")
				}
				closeoutCross(t, p, source, 2)
				p.checkKeyboard(t, source)
			})
		}
	}
}

func TestStage3CloseoutActiveGeometryChangeRestoresBothDirections(t *testing.T) {
	for _, source := range []int{0, 1} {
		for _, changed := range []int{0, 1} {
			t.Run([]string{"mac-source", "windows-source"}[source]+"/"+[]string{"mac-layout", "windows-layout"}[changed], func(t *testing.T) {
				p := newStage3Pair(t, "left")
				closeoutCross(t, p, source, 1)
				p.checkKeyboard(t, source)
				layout, err := pointer.NewTopology([]pointer.Screen{{ID: []string{"mac-resized", "windows-resized"}[changed], Width: 1600, Height: 900}})
				if err != nil {
					t.Fatal(err)
				}
				p.geometry[changed].updated.Store(&layout)
				closeoutLocal(t, p, source, 1)
				nativeWait(t, func() bool { return p.links[1-changed].RemoteScreen().Screen.Key() == layout.Bounds.Key() })
				time.Sleep(40 * time.Millisecond)
				if p.geometry[source].hidden.Load() != 1 {
					t.Fatal("布局更新重放旧控制")
				}
				closeoutCross(t, p, source, 2)
				p.checkKeyboard(t, source)
			})
		}
	}
}
