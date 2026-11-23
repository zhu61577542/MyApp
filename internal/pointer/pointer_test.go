package pointer

import (
	"testing"
	"time"
)

func TestFourDirectionsAndReturnWithNegativeOrigin(t *testing.T) {
	screen := Screen{ID: "primary", X: -100, Y: -50, Width: 200, Height: 100}
	for _, test := range []struct {
		side   string
		dx, dy int
	}{{"right", -5, 0}, {"left", 5, 0}, {"down", 0, -5}, {"up", 0, 5}} {
		entry := Entry{Side: test.side, Position: 0.5, ScreenKey: screen.Key()}
		travel, err := NewTravel(screen, entry)
		if err != nil || !screen.Contains(travel.Point) {
			t.Fatalf("落点错误: %s %v", test.side, err)
		}
		point, back := travel.Move(test.dx, test.dy)
		if !back || !screen.Contains(point) {
			t.Fatalf("返回边缘错误: %s %+v", test.side, point)
		}
		entry.Position = travel.ReturnRatio()
		landing, err := Landing(screen, entry, true)
		if err != nil || !screen.Contains(landing) {
			t.Fatal("本机返回落点无效")
		}
	}
}

func TestEdgeDelayAndReentryHysteresis(t *testing.T) {
	screen := Screen{ID: "s", Width: 100, Height: 100}
	neighbors := map[string]string{"right": "peer"}
	now := time.Now()
	w := &Watcher{}
	if _, _, ok := w.Observe(Point{99, 50}, screen, neighbors, 150*time.Millisecond, now); ok {
		t.Fatal("启用时鼠标已在边缘，不应立即捕获")
	}
	w.Observe(Point{50, 50}, screen, neighbors, 150*time.Millisecond, now)
	if _, _, ok := w.Observe(Point{99, 50}, screen, neighbors, 150*time.Millisecond, now); ok {
		t.Fatal("未满足停留时间")
	}
	id, entry, ok := w.Observe(Point{99, 50}, screen, neighbors, 150*time.Millisecond, now.Add(160*time.Millisecond))
	if !ok || id != "peer" || entry.Side != "right" {
		t.Fatal("没有触发正确目标")
	}
	if _, _, ok := w.Observe(Point{99, 50}, screen, neighbors, 0, now.Add(time.Second)); ok {
		t.Fatal("返回后未离开边缘就重新捕获")
	}
}

func TestInvalidGeometryAndEntryRejected(t *testing.T) {
	screen := Screen{ID: "s", Width: 100, Height: 100}
	if _, err := NewTravel(screen, Entry{Side: "right", Position: 2, ScreenKey: "x"}); err == nil {
		t.Fatal("接受了越界入口")
	}
	screen.Width = 0
	if screen.Valid() {
		t.Fatal("接受了无效屏幕")
	}
}
