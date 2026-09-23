package main

import (
	"testing"

	common "myapp/internal/input"
)

func TestTargetShortcutConsumesDigitAndRepeat(t *testing.T) {
	var targets []int
	handle := newTargetKeys(2, func(index int) error { targets = append(targets, index); return nil })
	for _, code := range []uint16{224, 226} {
		if used, err := handle(common.Event{Kind: common.KeyDown, Code: code}); used || err != nil {
			t.Fatal("修饰键错误")
		}
	}
	for _, kind := range []common.Kind{common.KeyDown, common.KeyDown, common.KeyUp} {
		if used, err := handle(common.Event{Kind: kind, Code: 31}); !used || err != nil {
			t.Fatal("切换键泄漏到目标")
		}
	}
	if len(targets) != 1 || targets[0] != 1 {
		t.Fatalf("切换目标错误: %v", targets)
	}
	if used, _ := handle(common.Event{Kind: common.KeyDown, Code: 32}); used {
		t.Fatal("吞掉了未配置目标的快捷键")
	}
}
