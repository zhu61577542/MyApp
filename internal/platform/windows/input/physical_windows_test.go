//go:build windows

package wininput

import (
	common "myapp/internal/input"
	"testing"
)

func TestPhysicalObservationFiltersInjectedEvents(t *testing.T) {
	for _, message := range []uint32{wmMouseMove, wmLeftDown, wmLeftUp, wmMouseWheel} {
		if _, ok := physicalMouseEvent(message, &mouseHookData{Flags: llMouseInjected}); ok {
			t.Fatal("注入的鼠标事件触发实体监听")
		}
	}
	for _, message := range []uint32{wmKeyDown, wmKeyUp, wmSysKeyDown} {
		if _, ok := physicalKeyboardEvent(message, &keyboardHookData{Flags: llKeyboardInjected}); ok {
			t.Fatal("注入的键盘事件触发实体监听")
		}
	}
	if e, ok := physicalMouseEvent(wmMouseMove, &mouseHookData{Point: nativePoint{-500, 100}}); !ok || e.Kind != common.MouseAbsolute || e.X != -500 {
		t.Fatal("未识别扩展屏上的实体移动")
	}
	if e, ok := physicalKeyboardEvent(wmKeyDown, &keyboardHookData{ScanCode: 0x1e}); !ok || e.Kind != common.KeyDown || e.Code != 4 {
		t.Fatal("未识别实体按键")
	}
	if e, ok := physicalMouseEvent(wmLeftDown, &mouseHookData{}); !ok || e.Kind != common.MouseButtonDown || e.Code != 1 {
		t.Fatal("未识别实体左键")
	}
}
