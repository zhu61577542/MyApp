package layout

import "testing"

func TestHitAndLanding(t *testing.T) {
	layout, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Set(Right, "right-device"); err != nil {
		t.Fatal(err)
	}
	if err := layout.Set(Top, "top-device"); err != nil {
		t.Fatal(err)
	}
	display := Display{Width: 1920, Height: 1080}
	switchTarget, ok := layout.Hit(Point{X: 1919, Y: 540}, display, 2)
	if !ok || switchTarget.DeviceID != "right-device" || switchTarget.Side != Right {
		t.Fatalf("边缘命中错误: %+v ok=%v", switchTarget, ok)
	}
	landing, err := Landing(switchTarget.Side, switchTarget.Position, Display{Width: 2560, Height: 1440})
	if err != nil {
		t.Fatal(err)
	}
	if landing.X != 1 || landing.Y < 719 || landing.Y > 721 {
		t.Fatalf("远端落点错误: %+v", landing)
	}
}

func TestCornerUsesNearestConfiguredEdge(t *testing.T) {
	layout, _ := New(2)
	layout.Set(Left, "left")
	layout.Set(Top, "top")
	target, ok := layout.Hit(Point{X: 3, Y: 1}, Display{Width: 100, Height: 100}, 5)
	if !ok || target.Side != Top {
		t.Fatalf("角落选择错误: %+v", target)
	}
}

func TestRejectsDuplicateAndExcessNeighbors(t *testing.T) {
	layout, _ := New(2)
	if err := layout.Set(Left, "a"); err != nil {
		t.Fatal(err)
	}
	if err := layout.Set(Right, "a"); err == nil {
		t.Fatal("同一设备占用两个方向未被拒绝")
	}
	if err := layout.Set(Right, "b"); err != nil {
		t.Fatal(err)
	}
	if err := layout.Set(Top, "c"); err == nil {
		t.Fatal("第三个相邻设备未被拒绝")
	}
}
