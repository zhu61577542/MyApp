package app

import (
	"context"
	"encoding/json"
	"myapp/internal/pointer"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOptionalTopologyHandshakeAndLegacyScreenShape(t *testing.T) {
	a, _ := NewSplitLink(nil, nil)
	b, _ := NewSplitLink(nil, nil)
	a.EnableDesktop(Capabilities{}, nil, nil)
	b.EnableDesktop(Capabilities{Control: true}, nil, func(context.Context, string) error { return nil })
	primary := pointer.Screen{ID: "p", Width: 100, Height: 100}
	topology, _ := pointer.NewTopology([]pointer.Screen{primary, {ID: "s", X: -80, Y: -20, Width: 80, Height: 120}})
	ready := make(chan struct{}, 1)
	first := make(chan ScreenInfo, 1)
	var changes atomic.Uint32
	a.EnableScreen(nil, func(info ScreenInfo) {
		if info.Screen.Valid() {
			select {
			case first <- info:
			default:
			}
		}
		if info.Topology != nil {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	})
	a.SetScreenChangeHandler(func() { changes.Add(1) })
	b.EnableScreen(func() (pointer.Screen, error) { return primary, nil }, nil)
	b.EnableTopology(func() (pointer.Topology, error) { return topology, nil })
	b.SetEdgeControlHandler(func(context.Context, string, *pointer.Entry) error { return nil })
	ctx := runDesktopLinks(t, a, b)
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("没有收到全部显示器布局")
	}
	info := a.RemoteScreen()
	if initial := <-first; initial.Topology == nil {
		t.Fatal("完整拓扑前提前公布了临时主屏就绪状态")
	}
	if info.Screen != topology.Bounds {
		t.Fatal("仍然限制为主屏幕")
	}
	if err := a.RequestEdgeControl(ctx, pointer.Entry{Side: "right", Position: 0.5, ScreenKey: info.Screen.Key()}); err != nil {
		t.Fatal(err)
	}
	if changes.Load() != 0 {
		t.Fatal("初始握手被误报为运行中的布局变化")
	}
	legacy, _ := json.Marshal(info)
	if strings.Contains(string(legacy), "topology") {
		t.Fatal("改变了旧版本严格解码的消息格式")
	}
}
