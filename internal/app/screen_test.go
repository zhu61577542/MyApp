package app

import (
	"context"
	"myapp/internal/pointer"
	"testing"
)

func TestScreenHandshakeBeforeEdgeControl(t *testing.T) {
	a, _ := NewSplitLink(nil, nil)
	b, _ := NewSplitLink(nil, nil)
	a.EnableDesktop(Capabilities{}, nil, nil)
	b.EnableDesktop(Capabilities{Control: true}, nil, func(context.Context, string) error { return nil })
	screen := pointer.Screen{ID: "remote", X: -100, Width: 1920, Height: 1080}
	ready := make(chan struct{}, 1)
	a.EnableScreen(nil, func(info ScreenInfo) {
		if info.Screen.Valid() {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	})
	b.EnableScreen(func() (pointer.Screen, error) { return screen, nil }, nil)
	entered := make(chan pointer.Entry, 1)
	b.SetEdgeControlHandler(func(_ context.Context, _ string, entry *pointer.Entry) error { entered <- *entry; return nil })
	ctx := runDesktopLinks(t, a, b)
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("没有收到真实屏幕能力")
	}
	entry := pointer.Entry{Side: "right", Position: 0.4, ScreenKey: screen.Key()}
	if err := a.RequestEdgeControl(ctx, entry); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-entered:
		if got != entry {
			t.Fatalf("入口未保留: %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("没有处理跨屏入口")
	}
	entry.ScreenKey = "obsolete"
	if a.RequestEdgeControl(ctx, entry) == nil {
		t.Fatal("接受了过期屏幕几何")
	}
}

func TestPeerWithoutScreenCapabilityKeepsManualControl(t *testing.T) {
	a, _ := NewSplitLink(nil, nil)
	b, _ := NewSplitLink(nil, nil)
	a.EnableDesktop(Capabilities{}, nil, nil)
	b.EnableDesktop(Capabilities{Control: true}, nil, func(context.Context, string) error { return nil })
	a.EnableScreen(func() (pointer.Screen, error) { return pointer.Screen{ID: "local", Width: 100, Height: 100}, nil }, nil)
	ctx := runDesktopLinks(t, a, b)
	if a.RequestEdgeControl(ctx, pointer.Entry{Side: "right", Position: 0.5, ScreenKey: "unknown"}) == nil {
		t.Fatal("向不支持的对端发送了扩展入口")
	}
	if err := a.RequestControl(ctx, "begin"); err != nil {
		t.Fatalf("可选屏幕消息破坏了普通控制: %v", err)
	}
}
