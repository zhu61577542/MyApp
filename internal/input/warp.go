package input

import (
	"sync/atomic"
	"time"
)

type cursorWarp struct {
	x, y  int32
	until time.Time
}

var expectedCursorWarp atomic.Pointer[cursorWarp]

// ExpectCursorWarp 排除本进程恢复光标产生的未标记移动。
func ExpectCursorWarp(x, y int32) func() {
	w := &cursorWarp{x: x, y: y, until: time.Now().Add(250 * time.Millisecond)}
	expectedCursorWarp.Store(w)
	return func() { expectedCursorWarp.CompareAndSwap(w, nil) }
}

func ConsumeCursorWarp(x, y int32) bool {
	w := expectedCursorWarp.Load()
	if w == nil {
		return false
	}
	if time.Now().After(w.until) {
		expectedCursorWarp.CompareAndSwap(w, nil)
		return false
	}
	return w.x == x && w.y == y && expectedCursorWarp.CompareAndSwap(w, nil)
}
