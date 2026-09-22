package input

import (
	"bytes"
	"math"
	"reflect"
	"testing"
)

func TestEventRoundTrip(t *testing.T) {
	want := Event{Kind: KeyDown, Sequence: 42, Code: 224}
	var buffer bytes.Buffer
	if err := want.Encode(&buffer); err != nil {
		t.Fatal(err)
	}
	if buffer.Len() != WireSize {
		t.Fatalf("线格式长度=%d", buffer.Len())
	}
	got, err := Decode(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("事件不一致: got=%+v want=%+v", got, want)
	}
}

func TestCoalesceOnlyConsecutiveMouseMoves(t *testing.T) {
	events := []Event{
		{Kind: MouseRelative, Sequence: 1, X: math.MaxInt32, Y: 1},
		{Kind: MouseRelative, Sequence: 2, X: 10, Y: 2},
		{Kind: KeyDown, Sequence: 3, Code: 4},
		{Kind: MouseAbsolute, Sequence: 4, X: 10, Y: 20},
		{Kind: MouseAbsolute, Sequence: 5, X: 30, Y: 40},
	}
	got := Coalesce(events)
	want := []Event{
		{Kind: MouseRelative, Sequence: 2, X: math.MaxInt32, Y: 3},
		{Kind: KeyDown, Sequence: 3, Code: 4},
		{Kind: MouseAbsolute, Sequence: 5, X: 30, Y: 40},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("合并错误: got=%+v want=%+v", got, want)
	}
}

func TestTrackerProducesDeterministicRelease(t *testing.T) {
	tracker := NewTracker()
	for _, event := range []Event{
		{Kind: KeyDown, Sequence: 1, Code: 225},
		{Kind: KeyDown, Sequence: 2, Code: 4},
		{Kind: MouseButtonDown, Sequence: 3, Code: 2},
	} {
		tracker.Apply(event)
	}
	releases, next := tracker.ReleaseEvents(10)
	want := []Event{
		{Kind: KeyUp, Sequence: 10, Code: 4},
		{Kind: KeyUp, Sequence: 11, Code: 225},
		{Kind: MouseButtonUp, Sequence: 12, Code: 2},
	}
	if !reflect.DeepEqual(releases, want) || next != 13 {
		t.Fatalf("释放错误: events=%+v next=%d", releases, next)
	}
	keys, buttons := tracker.Counts()
	if keys != 0 || buttons != 0 {
		t.Fatalf("状态未清空: keys=%d buttons=%d", keys, buttons)
	}
}

func TestRejectsInvalidEvents(t *testing.T) {
	for _, event := range []Event{
		{},
		{Kind: KeyDown, Sequence: 1, Code: 1},
		{Kind: MouseButtonDown, Sequence: 1, Code: 6},
		{Kind: MouseScroll, Sequence: 1},
	} {
		if err := event.Validate(); err == nil {
			t.Fatalf("无效事件未被拒绝: %+v", event)
		}
	}
}
