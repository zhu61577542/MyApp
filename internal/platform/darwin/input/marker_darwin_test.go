//go:build darwin

package darwininput

import (
	common "myapp/internal/input"
	"testing"
)

func TestMyAppInjectionPassesThroughWithoutCaptureOrModifierChanges(t *testing.T) {
	for _, observing := range []bool{false, true} {
		calls := 0
		c := &Capturer{observing: observing, modifierDown: make(map[uint16]bool), handler: func(common.Event) error { calls++; return nil }, getInteger: func(_ uintptr, field uint32) int64 {
			if field == eventSourceUserData {
				return myAppEventMarker
			}
			return 56
		}}
		if got := c.handleEvent(12, 123); got != 123 || calls != 0 || len(c.modifierDown) != 0 {
			t.Fatal("本程序注入事件被捕获、吞掉或改变修饰键状态")
		}
	}
}

func TestObservePhysicalEventDoesNotSuppressIt(t *testing.T) {
	for _, observing := range []bool{false, true} {
		calls := 0
		c := &Capturer{observing: observing, handler: func(event common.Event) error {
			calls++
			if event.Kind != common.MouseRelative {
				t.Fatal(event)
			}
			return nil
		}, getInteger: func(_ uintptr, field uint32) int64 {
			if field == 4 {
				return 2
			}
			return 0
		}}
		got := c.handleEvent(5, 123)
		if calls != 1 || observing && got != 123 || !observing && got != 0 {
			t.Fatal("被动监听没有放行实体输入，或主动捕获未拦截")
		}
	}
}

func TestAllInjectedKindsAndReleasesCarryMarker(t *testing.T) {
	marked, posts := 0, 0
	i := &Injector{tracker: common.NewTracker(), pressedMouseButton: make(map[uint16]struct{}), createMouse: func(uintptr, uint32, point, uint32) uintptr { return 1 }, createKeyboard: func(uintptr, uint16, bool) uintptr { return 1 }, createScroll: func(uintptr, uint32, uint32, int32, int32, int32) uintptr { return 1 }, createEvent: func(uintptr) uintptr { return 1 }, getLocation: func(uintptr) point { return point{} }, release: func(uintptr) {}, setInteger: func(_ uintptr, field uint32, value int64) {
		if field != eventSourceUserData || value != myAppEventMarker {
			t.Fatal("标记字段错误")
		}
		marked++
	}, post: func(uint32, uintptr) {
		posts++
		if marked != posts {
			t.Fatal("注入前没有标记")
		}
	}}
	for _, event := range []common.Event{{Kind: common.MouseAbsolute, Sequence: 1, X: 20, Y: 30}, {Kind: common.MouseRelative, Sequence: 2, X: 1, Y: 1}, {Kind: common.MouseButtonDown, Sequence: 3, Code: 1}, {Kind: common.MouseScroll, Sequence: 4, Y: 1}, {Kind: common.KeyDown, Sequence: 5, Code: 4}, {Kind: common.ReleaseAll, Sequence: 6}} {
		if err := i.Inject(event); err != nil {
			t.Fatal(err)
		}
	}
	if posts != 7 {
		t.Fatalf("移动/按钮/滚轮/键盘/释放未完整验证: %d", posts)
	}
}

func TestHeldModifiersSeedCorrectReleaseWithoutTypingOrdinaryKeys(t *testing.T) {
	for _, observing := range []bool{false, true} {
		var events []common.Event
		c := &Capturer{observing: observing, sequence: 1, modifierDown: make(map[uint16]bool), keyState: func(state int32, key uint16) bool {
			if state != 1 {
				t.Fatal("未使用硬件状态表")
			}
			return key == 59 || key == 0
		}, buttonState: func(int32, uint32) bool { return false }, handler: func(e common.Event) error { events = append(events, e); return nil }, getInteger: func(_ uintptr, field uint32) int64 {
			if field == 9 {
				return 59
			}
			return 0
		}}
		c.seedPhysicalState()
		if len(events) == 0 {
			t.Fatal("没有接续已按下的修饰键")
		}
		if !observing {
			for _, event := range events {
				if event.Code == 4 {
					t.Fatal("切换时重复输入了普通字符")
				}
			}
		}
		c.handleEvent(12, 123)
		last := events[len(events)-1]
		if last.Kind != common.KeyUp || last.Code != 224 {
			t.Fatal("已按住Ctrl的首次释放被当成按下", last)
		}
	}
}
