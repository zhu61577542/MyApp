package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	common "myapp/internal/input"
)

type bidirectionalProbeInjector struct {
	active   atomic.Bool
	released atomic.Int32
	events   chan common.Event
}

func (i *bidirectionalProbeInjector) Inject(event common.Event) error {
	if !i.active.Load() {
		return errors.New("未确认控制就收到输入")
	}
	i.events <- event
	return nil
}

func (i *bidirectionalProbeInjector) ReleaseAll() error {
	i.active.Store(false)
	i.released.Add(1)
	return nil
}

func (i *bidirectionalProbeInjector) Close() error { return i.ReleaseAll() }

func TestSplitLinkSupportsSequentialControlInBothDirections(t *testing.T) {
	left := &bidirectionalProbeInjector{events: make(chan common.Event, 4)}
	right := &bidirectionalProbeInjector{events: make(chan common.Event, 4)}
	a, _ := NewSplitLink(left, nil)
	b, _ := NewSplitLink(right, nil)
	for index, link := range []*Link{a, b} {
		target := []*bidirectionalProbeInjector{left, right}[index]
		link.EnableDesktop(Capabilities{Control: true}, nil, func(_ context.Context, action string) error {
			if action == "begin" {
				target.active.Store(true)
				return nil
			}
			return target.ReleaseAll()
		})
	}
	ctx := runDesktopLinks(t, a, b)
	for index, source := range []*Link{a, b} {
		target := []*bidirectionalProbeInjector{right, left}[index]
		if err := source.RequestControl(ctx, "begin"); err != nil {
			t.Fatal(err)
		}
		events := []common.Event{{Kind: common.MouseAbsolute, Sequence: 1, X: 20, Y: 30}, {Kind: common.KeyDown, Sequence: 2, Code: 4}, {Kind: common.KeyUp, Sequence: 3, Code: 4}}
		for _, event := range events {
			if err := source.SendInput(ctx, event); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-target.events:
				if got != event {
					t.Fatalf("方向%d输入被改变: %+v", index, got)
				}
			case <-ctx.Done():
				t.Fatalf("方向%d没有收到输入", index)
			}
		}
		if err := source.RequestControl(ctx, "end"); err != nil {
			t.Fatal(err)
		}
		if target.active.Load() || target.released.Load() == 0 {
			t.Fatal("控制交接前没有释放目标")
		}
	}
}
