//go:build darwin

package darwininput

import (
	common "myapp/internal/input"
	"testing"
)

func TestCapsLockTapProducesOneLatchedTransitionAndNoCleanupToggle(t *testing.T) {
	for _, initial := range []bool{false, true} {
		nativeState := initial
		var transitions []bool
		i := &Injector{tracker: common.NewTracker(), pressedMouseButton: make(map[uint16]struct{}), flagsState: func(int32) uint64 {
			if nativeState {
				return 1 << 16
			}
			return 0
		}, createKeyboard: func(_ uintptr, key uint16, down bool) uintptr {
			if key != 57 {
				t.Fatal("虚拟键映射错误")
			}
			transitions = append(transitions, down)
			return 1
		}, setInteger: func(uintptr, uint32, int64) {}, post: func(uint32, uintptr) { nativeState = transitions[len(transitions)-1] }, release: func(uintptr) {}}
		send := func(kind common.Kind) {
			t.Helper()
			if err := i.Inject(common.Event{Kind: kind, Code: 57, Sequence: 1}); err != nil {
				t.Fatal(err)
			}
		}
		send(common.KeyDown)
		send(common.KeyDown)
		send(common.KeyUp)
		if len(transitions) != 1 || transitions[0] == initial {
			t.Fatal("一次按压产生多次转换或重复按下被翻转")
		}
		send(common.KeyDown)
		if len(transitions) != 2 || transitions[1] != initial {
			t.Fatal("第二次按压未切换回原锁定状态")
		}
		if err := i.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		if len(transitions) != 2 {
			t.Fatal("恢复释放额外翻转CapsLock")
		}
		// 本机在两次共享之间改变锁定状态，下一次从真实状态重新开始。
		nativeState = !initial
		send(common.KeyDown)
		send(common.KeyUp)
		if len(transitions) != 3 || transitions[2] != initial {
			t.Fatal("新租约未重读本机锁定状态")
		}
	}
}

func TestCapsLockCreateFailureDoesNotCommitOrFlipDuringCleanup(t *testing.T) {
	fail := true
	var transitions []bool
	i := &Injector{tracker: common.NewTracker(), pressedMouseButton: make(map[uint16]struct{}), flagsState: func(int32) uint64 { return 0 }, createKeyboard: func(_ uintptr, _ uint16, down bool) uintptr {
		if fail {
			return 0
		}
		transitions = append(transitions, down)
		return 1
	}, setInteger: func(uintptr, uint32, int64) {}, post: func(uint32, uintptr) {}, release: func(uintptr) {}}
	e := common.Event{Kind: common.KeyDown, Code: 57, Sequence: 1}
	if err := i.Inject(e); err == nil || i.capsHeld || i.capsOn {
		t.Fatal("失败创建仍提交锁定状态")
	}
	fail = false
	if err := i.Inject(e); err != nil {
		t.Fatal(err)
	}
	if err := i.Inject(e); err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 || !transitions[0] {
		t.Fatal("重试或重复按下产生多次转换")
	}
	if err := i.Close(); err != nil {
		t.Fatal(err)
	}
	if len(transitions) != 1 {
		t.Fatal("关闭时翻转Caps")
	}
}
