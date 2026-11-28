package pointer

import (
	"testing"
	"time"
)

func TestPhysicalEdgeReentryRequiresOutwardIntent(t *testing.T) {
	screen := Screen{ID: "desktop", X: -1080, Y: -493, Width: 3640, Height: 1933}
	for _, tc := range []struct {
		side   string
		point  Point
		dx, dy int
	}{
		{"right", Point{2559, 500}, 1, 0}, {"left", Point{-1080, 500}, -1, 0},
		{"up", Point{500, -493}, 0, -1}, {"down", Point{500, 1439}, 0, 1},
	} {
		t.Run(tc.side, func(t *testing.T) {
			neighbors := map[string]string{tc.side: "other"}
			var w Watcher
			now := time.Now()
			if _, _, hit := w.Observe(tc.point, screen, neighbors, 0, now); hit {
				t.Fatal("静态入口反弹")
			}
			if _, _, hit := w.ObservePhysical(tc.point, screen, neighbors, 0, now, -tc.dx, -tc.dy); hit {
				t.Fatal("朝内移动触发跨屏")
			}
			if _, entry, hit := w.ObservePhysical(tc.point, screen, neighbors, 0, now, tc.dx, tc.dy); !hit || entry.Side != tc.side {
				t.Fatal("实体朝外移动未解除入口滞回")
			}
			if _, _, hit := w.Observe(tc.point, screen, neighbors, 0, now); hit {
				t.Fatal("无新实体移动再次跨屏")
			}
			if _, _, hit := w.ObservePhysical(tc.point, screen, neighbors, 0, now, 0, 0); !hit {
				t.Fatal("系统夹住的实体移动无法接续")
			}
		})
	}
}
