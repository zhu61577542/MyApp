//go:build windows

package wininput

import (
	common "myapp/internal/input"
	"strings"
	"testing"
)

func TestUnsupportedKeyDoesNotRevealCode(t *testing.T) {
	i := &Injector{}
	_, err := i.translate(common.Event{Kind: common.KeyDown, Code: 65535})
	if err == nil || strings.Contains(err.Error(), "65535") {
		t.Fatal("错误信息包含原始按键码")
	}
}

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

func TestPhysicalMotionTracksInjectedLandingAndFiltersWarp(t *testing.T) {
	var c Capturer
	if _, ok := c.physicalMotion(&mouseHookData{Point: nativePoint{2557, 100}, Flags: llMouseInjected}); ok {
		t.Fatal("注入落点触发接管")
	}
	e, ok := c.physicalMotion(&mouseHookData{Point: nativePoint{2559, 100}})
	if !ok || e.Kind != common.MouseRelative || e.X != 2 || e.Y != 0 {
		t.Fatal("未按注入后落点计算实体方向", e)
	}
	if _, ok := c.physicalMotion(&mouseHookData{Point: nativePoint{100, 100}, Flags: llMouseInjected}); ok {
		t.Fatal("注入产生了实体移动")
	}
	if x, y, known := c.PhysicalPosition(); !known || x != 2559 || y != 100 {
		t.Fatal("晚到注入替换了实体落点")
	}
	cancel := common.ExpectCursorWarp(2557, 100)
	defer cancel()
	if _, ok := c.physicalMotion(&mouseHookData{Point: nativePoint{2557, 100}}); ok {
		t.Fatal("程序恢复位置被当成实体移动")
	}
	e, ok = c.physicalMotion(&mouseHookData{Point: nativePoint{2559, 100}})
	if !ok || e.X != 2 {
		t.Fatal("恢复后实体方向错误", e)
	}
	e, ok = c.physicalMotion(&mouseHookData{Point: nativePoint{2559, 100}})
	if !ok || e.X != 0 {
		t.Fatal("边缘夹住后丢失实体意图")
	}
}

func TestFirstUnseededPhysicalMotionOnlyEstablishesBaseline(t *testing.T) {
	var c Capturer
	if _, ok := c.physicalMotion(&mouseHookData{Point: nativePoint{2558, 100}}); ok {
		t.Fatal("无方向基线的首移动被当成朝外")
	}
	e, ok := c.physicalMotion(&mouseHookData{Point: nativePoint{2557, 100}})
	if !ok || e.X != -1 {
		t.Fatal("朝内方向错误")
	}
}
