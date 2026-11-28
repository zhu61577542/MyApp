package pointer

import (
	"fmt"
	"testing"
	"time"
)

func TestDifferentRuntimeDesktopGeometriesCrossAndReturn(t *testing.T) {
	sets := [][]Screen{
		{{ID: "retina", Width: 1512, Height: 982}},
		{{ID: "primary", Width: 3840, Height: 2160}, {ID: "portrait", X: -1080, Y: -493, Width: 1080, Height: 1920}},
		{{ID: "main", Width: 1728, Height: 1117}, {ID: "external", X: 1728, Y: -300, Width: 2560, Height: 1440}},
		{{ID: "portrait", Width: 1440, Height: 2560}, {ID: "wide", X: -2560, Y: 200, Width: 2560, Height: 1440}},
	}
	for a, displays := range sets {
		for b, targetDisplays := range sets {
			for _, side := range []string{"left", "right", "up", "down"} {
				t.Run(fmt.Sprintf("%d_%d_%s", a, b, side), func(t *testing.T) {
					source, err := NewTopology(displays)
					if err != nil {
						t.Fatal(err)
					}
					target, err := NewTopology(targetDisplays)
					if err != nil {
						t.Fatal(err)
					}
					var point Point
					found := false
					for _, d := range displays {
						point = Point{d.X + d.Width/2, d.Y + d.Height/2}
						switch side {
						case "left":
							point.X = d.X
							found = d.X == source.Bounds.X
						case "right":
							point.X = d.X + d.Width - 1
							found = d.X+d.Width == source.Bounds.X+source.Bounds.Width
						case "up":
							point.Y = d.Y
							found = d.Y == source.Bounds.Y
						case "down":
							point.Y = d.Y + d.Height - 1
							found = d.Y+d.Height == source.Bounds.Y+source.Bounds.Height
						}
						if found {
							break
						}
					}
					if !found {
						t.Fatal("没有真实外边缘")
					}
					dx, dy := 0, 0
					switch side {
					case "left":
						dx = -1
					case "right":
						dx = 1
					case "up":
						dy = -1
					case "down":
						dy = 1
					}
					var watcher Watcher
					_, entry, hit := watcher.ObservePhysical(point, source.Bounds, map[string]string{side: "target"}, 0, time.Now(), dx, dy)
					if !hit {
						t.Fatal("按实时布局无法触边")
					}
					entry.ScreenKey = target.Bounds.Key()
					travel, err := NewTopologyTravel(target, entry)
					if err != nil {
						t.Fatal(err)
					}
					inside := func(point Point, topology Topology) bool {
						for _, d := range topology.Displays {
							if d.Contains(point) {
								return true
							}
						}
						return false
					}
					if !inside(travel.Point, target) {
						t.Fatal("落点位于显示器空隙")
					}
					_, back := travel.Move(-dx*100000, -dy*100000)
					if !back {
						t.Fatal("无法从原入口返回")
					}
					entry.ScreenKey = source.Bounds.Key()
					entry.Position = travel.ReturnRatio()
					returned, err := source.Landing(entry, true)
					if err != nil || !inside(returned, source) {
						t.Fatal("返程未适配来源布局", err)
					}
				})
			}
		}
	}
}
