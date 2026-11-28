package input

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestScalerPreservesSmallSignedMotionAndSeparateScroll(t *testing.T) {
	s, err := NewScaler(Settings{PointerPercent: 10, ScrollPercent: 250})
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	emit := func(e Event) error { events = append(events, e); return nil }
	for range 10 {
		if err := s.Forward(Event{Kind: MouseRelative, X: 1, Y: -1}, emit); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 1 || events[0].X != 1 || events[0].Y != -1 {
		t.Fatalf("慢速微移丢失或符号错误: %+v", events)
	}
	for range 2 {
		_ = s.Forward(Event{Kind: MouseScroll, X: -1, Y: 1}, emit)
	}
	if len(events) != 3 || events[1].X != -2 || events[1].Y != 2 || events[2].X != -3 || events[2].Y != 3 {
		t.Fatalf("滚动倍率与移动耦合或余量丢失: %+v", events)
	}
	_ = s.Forward(Event{Kind: MouseRelative, X: 9, Y: -9}, emit)
	_ = s.Forward(Event{Kind: MouseRelative, X: -9, Y: 9}, emit)
	if len(events) != 3 {
		t.Fatal("方向反转的未满像素余量没有抵消")
	}
}

func TestScalerScrollChunksStayValidAndStopOnFailure(t *testing.T) {
	s, _ := NewScaler(Settings{PointerPercent: 100, ScrollPercent: 400})
	x, y, calls := int32(0), int32(0), 0
	err := s.Forward(Event{Kind: MouseScroll, Sequence: 1, X: -75, Y: 100}, func(e Event) error {
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
		x += e.X
		y += e.Y
		calls++
		return nil
	})
	if err != nil || x != -300 || y != 400 || calls != 4 {
		t.Fatal(x, y, calls, err)
	}
	want := errors.New("发送失败")
	calls = 0
	err = s.Forward(Event{Kind: MouseScroll, Y: 100}, func(Event) error { calls++; return want })
	if !errors.Is(err, want) || calls != 1 {
		t.Fatal("发送错误后仍继续滚动", calls, err)
	}
}

func TestScalerDoesNotChangeKeysButtonsAbsoluteOrRelease(t *testing.T) {
	s, _ := NewScaler(Settings{PointerPercent: 10, ScrollPercent: 10})
	events := []Event{{Kind: KeyDown, Sequence: 1, Code: 4}, {Kind: KeyUp, Sequence: 2, Code: 4}, {Kind: MouseButtonDown, Sequence: 3, Code: 1}, {Kind: MouseButtonUp, Sequence: 4, Code: 1}, {Kind: ReleaseAll, Sequence: 5}, {Kind: MouseAbsolute, Sequence: 6, X: -100, Y: 22}}
	var got []Event
	for _, e := range events {
		_ = s.Forward(e, func(e Event) error { got = append(got, e); return nil })
	}
	if !reflect.DeepEqual(events, got) {
		t.Fatal("修改了非速度事件", got)
	}
	var small []Event
	for range 10 {
		_ = s.Forward(Event{Kind: MouseScroll, Y: -1}, func(e Event) error { small = append(small, e); return nil })
	}
	if len(small) != 1 || small[0].Y != -1 {
		t.Fatal("低倍率滚动丢失", small)
	}
}

func TestScalerSaturatesMotionAndRejectsInvalidSettings(t *testing.T) {
	s, _ := NewScaler(Settings{PointerPercent: 400, ScrollPercent: 100})
	_ = s.Forward(Event{Kind: MouseRelative, X: math.MaxInt32, Y: math.MinInt32}, func(e Event) error {
		if e.X != math.MaxInt32 || e.Y != math.MinInt32 {
			t.Fatal("移动溢出翻转", e)
		}
		return nil
	})
	if _, err := NewScaler(Settings{}); err == nil {
		t.Fatal("接受无效倍率")
	}
}
