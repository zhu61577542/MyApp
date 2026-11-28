package main

import (
	"context"
	"testing"
	"time"

	"myapp/internal/app"
	"myapp/internal/config"
	"myapp/internal/desktop"
	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
)

func TestPeerSourceSpeedBeforeTravelAndFixedForLease(t *testing.T) {
	for _, edge := range []bool{false, true} {
		t.Run(map[bool]string{false: "relative", true: "edge"}[edge], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			state := desktop.New(config.Default("source", config.RoleAgent), "source")
			state.Update(func(s *desktop.Snapshot) {
				s.EdgeEnabled = true
				s.InputSettings = common.Settings{PointerPercent: 50, ScrollPercent: 200}
			})
			geometry := &peerNativePointer{}
			var capture *peerNativeCapture
			screen, _ := geometry.Screen()
			d := &desktopPeerDriver{state: state, geometry: func() (pointer.Backend, error) { return geometry, nil },
				remote: func(string) app.ScreenInfo { return app.ScreenInfo{Screen: screen} },
				createCapture: func() (common.Capturer, error) {
					capture = &peerNativeCapture{events: make(chan common.Event, 8)}
					return capture, nil
				}}
			var entry *pointer.Entry
			if edge {
				entry = &pointer.Entry{Side: "right", Position: 0.5, ScreenKey: screen.Key()}
			}
			emitted := make(chan common.Event, 16)
			next := func() common.Event {
				select {
				case e := <-emitted:
					return e
				case <-ctx.Done():
					t.Fatal("未收到缩放后的输入")
					return common.Event{}
				}
			}
			for lease := uint64(1); lease <= 2; lease++ {
				token := ownership.Token{Fence: ownership.Fence{Session: "0123456789abcdef0123456789abcdef", Epoch: lease}, Source: "source", Target: "target", RequestID: lease}
				if err := d.Prepare(ctx, token, true, entry, func(e common.Event) error { emitted <- e; return nil }, func(bool) { t.Error("缩放意外停止控制") }); err != nil {
					t.Fatal(err)
				}
				if err := d.Activate(ctx, token); err != nil {
					t.Fatal(err)
				}
				if lease == 1 {
					state.Update(func(s *desktop.Snapshot) { s.InputSettings = common.Settings{PointerPercent: 200, ScrollPercent: 50} })
				}
				capture.events <- common.Event{Kind: common.MouseRelative, X: 10, Y: 2}
				e := next()
				wantX, wantY := int32(5), int32(1)
				if lease == 2 {
					wantX, wantY = 20, 4
				}
				kind := common.MouseRelative
				if edge {
					kind = common.MouseAbsolute
					wantX += 2
					wantY += 50
				}
				if e.Kind != kind || e.X != wantX || e.Y != wantY {
					t.Fatal("移动未在Travel前按租约倍率处理", e, wantX, wantY)
				}
				capture.events <- common.Event{Kind: common.MouseScroll, Y: 1}
				if lease == 2 {
					capture.events <- common.Event{Kind: common.MouseScroll, Y: 1}
				}
				e = next()
				wantScroll := int32(2)
				if lease == 2 {
					wantScroll = 1
				}
				if e.Kind != common.MouseScroll || e.Y != wantScroll {
					t.Fatal("滚动倍率错误", e)
				}
				capture.events <- common.Event{Kind: common.KeyUp, Code: 4}
				if e = next(); e.Kind != common.KeyUp || e.Code != 4 {
					t.Fatal("改变了键盘释放", e)
				}
				if err := d.Restore(ctx); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
