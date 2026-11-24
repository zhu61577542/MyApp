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
	if !back {
		t.Fatal("错位屏幕空隙阻止正常返回")
	}
	travel.Point = Point{X: -10, Y: -100}
	point, back = travel.Move(20, 0)
	if back || point.X >= 0 {
		t.Fatal("鼠标进入不存在的屏幕区域")
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
