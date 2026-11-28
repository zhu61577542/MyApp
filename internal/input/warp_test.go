package input

import "testing"

func TestExpectedCursorWarpIsExactOnceAndCancelable(t *testing.T) {
	cancel := ExpectCursorWarp(-100, 20)
	if ConsumeCursorWarp(-99, 20) {
		t.Fatal("误过滤实体移动")
	}
	if !ConsumeCursorWarp(-100, 20) || ConsumeCursorWarp(-100, 20) {
		t.Fatal("恢复光标未一次性过滤")
	}
	cancel()
	old := ExpectCursorWarp(1, 2)
	newer := ExpectCursorWarp(3, 4)
	old()
	if !ConsumeCursorWarp(3, 4) {
		t.Fatal("旧取消移除了新恢复标记")
	}
	newer()
	cancel = ExpectCursorWarp(5, 6)
	cancel()
	if ConsumeCursorWarp(5, 6) {
		t.Fatal("失败恢复遗留标记")
	}
}
