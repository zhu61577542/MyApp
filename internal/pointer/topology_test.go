package pointer

import "testing"

func TestWindowsPortraitSecondDisplayAndInternalSeam(t *testing.T) {
	topology, err := NewTopology([]Screen{{ID: "second", X: -1080, Y: -493, Width: 1080, Height: 1920}, {ID: "primary", Width: 2560, Height: 1440}})
	if err != nil {
		t.Fatal(err)
	}
	travel, err := NewTopologyTravel(topology, Entry{Side: "right", Position: 0.5, ScreenKey: topology.Bounds.Key()})
	if err != nil {
		t.Fatal(err)
	}
	if travel.Point.X >= 0 {
		t.Fatal("没有进入左侧扩展屏")
	}
	point, back := travel.Move(1200, 0)
	if back || point.X < 0 {
		t.Fatalf("内部接缝触发返回或无法进入主屏: %+v %t", point, back)
	}
	point, back = travel.Move(-200, 0)
	if back || point.X >= 0 {
		t.Fatalf("不能从主屏进入显示器2: %+v %t", point, back)
	}
	_, back = travel.Move(-1200, 0)
	if !back {
		t.Fatal("外部返回边缘未返回本机")
	}
	travel.Point = Point{X: 0, Y: 1435}
	_, back = travel.Move(-5, 0)
	if back {
		t.Fatal("错位屏幕凹口不应触发返回")
	}
	travel.Point = Point{X: -10, Y: -100}
	point, back = travel.Move(20, 0)
	if back || point.X >= 0 {
		t.Fatal("鼠标进入不存在的屏幕区域")
	}
}

func TestStaggeredDisplayNotchesDoNotReturnToSource(t *testing.T) {
	for _, test := range []struct {
		name, side string
		displays   []Screen
		point      Point
		dx, dy     int
	}{
		{"primary-upper-left", "left", []Screen{{ID: "primary", Width: 2560, Height: 1440}, {ID: "second", X: -1080, Y: -493, Width: 1080, Height: 1920}}, Point{-2, -20}, 5, 0},
		{"primary-lower-left", "right", []Screen{{ID: "primary", Width: 2560, Height: 1440}, {ID: "second", X: -1080, Y: -493, Width: 1080, Height: 1920}}, Point{2, 1435}, -5, 0},
		{"upper-display-notch", "up", []Screen{{ID: "primary", Width: 1440, Height: 2560}, {ID: "second", X: -493, Y: -1080, Width: 1920, Height: 1080}}, Point{-20, -2}, 0, 5},
		{"lower-display-notch", "down", []Screen{{ID: "primary", Width: 1440, Height: 2560}, {ID: "second", X: -493, Y: -1080, Width: 1920, Height: 1080}}, Point{1435, 2}, 0, -5},
	} {
		t.Run(test.name, func(t *testing.T) {
			topology, err := NewTopology(test.displays)
			if err != nil {
				t.Fatal(err)
			}
			travel, err := NewTopologyTravel(topology, Entry{Side: test.side, Position: 0.5, ScreenKey: topology.Bounds.Key()})
			if err != nil {
				t.Fatal(err)
			}
			travel.Point = test.point
			point, back := travel.Move(test.dx, test.dy)
			if back {
				t.Fatal("显示器凹口被误判为整台电脑的返程边缘")
			}
			inside := false
			for _, display := range topology.Displays {
				inside = inside || display.Contains(point)
			}
			if !inside {
				t.Fatal("光标落入显示器间空白区域")
			}
		})
	}
}

func TestInternalSeamNearPrimaryCornersKeepsTargetControl(t *testing.T) {
	topology, err := NewTopology([]Screen{{ID: "primary", Width: 2560, Height: 1440}, {ID: "second", X: -1080, Y: -493, Width: 1080, Height: 1920}})
	if err != nil {
		t.Fatal(err)
	}
	for _, side := range []string{"left", "right"} {
		for _, y := range []int{1, 100, 1400} {
			travel, err := NewTopologyTravel(topology, Entry{Side: side, Position: 0.5, ScreenKey: topology.Bounds.Key()})
			if err != nil {
				t.Fatal(err)
			}
			travel.Point = Point{2, y}
			point, back := travel.Move(-5, 0)
			if back || point != (Point{-3, y}) {
				t.Fatalf("主屏进入左侧扩展屏失败: %s %+v %t", side, point, back)
			}
			point, back = travel.Move(5, 0)
			if back || point != (Point{2, y}) {
				t.Fatalf("扩展屏返回主屏时误切换电脑: %s %+v %t", side, point, back)
			}
		}
	}
}

func TestTopologyValidationAndStableKey(t *testing.T) {
	a := Screen{ID: "a", Width: 100, Height: 100}
	b := Screen{ID: "b", X: 100, Width: 200, Height: 100}
	x, _ := NewTopology([]Screen{a, b})
	y, _ := NewTopology([]Screen{b, a})
	if !x.Valid() || x.Bounds != y.Bounds {
		t.Fatal("枚举顺序改变了布局键")
	}
	x.Bounds.Width++
	if x.Valid() {
		t.Fatal("接受不一致的桌面范围")
	}
	if _, err := NewTopology([]Screen{a, a}); err == nil {
		t.Fatal("接受重复显示器")
	}
}

func TestTopologyOuterEdgesKeepReturning(t *testing.T) {
	for _, test := range []struct {
		side     string
		vertical bool
		point    Point
		dx, dy   int
	}{
		{"left", false, Point{2558, 500}, 5, 0},
		{"right", false, Point{-1078, 500}, -5, 0},
		{"up", true, Point{500, 2558}, 0, 5},
		{"down", true, Point{500, -1078}, 0, -5},
	} {
		t.Run(test.side, func(t *testing.T) {
			displays := []Screen{{ID: "primary", Width: 2560, Height: 1440}, {ID: "second", X: -1080, Y: -493, Width: 1080, Height: 1920}}
			if test.vertical {
				for i := range displays {
					displays[i].X, displays[i].Y = displays[i].Y, displays[i].X
					displays[i].Width, displays[i].Height = displays[i].Height, displays[i].Width
				}
			}
			topology, err := NewTopology(displays)
			if err != nil {
				t.Fatal(err)
			}
			travel, err := NewTopologyTravel(topology, Entry{Side: test.side, Position: 0.5, ScreenKey: topology.Bounds.Key()})
			if err != nil {
				t.Fatal(err)
			}
			travel.Point = test.point
			if _, back := travel.Move(test.dx, test.dy); !back {
				t.Fatal("整组桌面的返程外边缘失效")
			}
		})
	}
}
