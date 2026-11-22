//go:build windows

package wininput

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"

	common "myapp/internal/input"
)

func TestNativeMouseHookMessages(t *testing.T) {
	if os.Getenv("MYAPP_WINDOWS_HOOK_TEST") != "1" {
		t.Skip("需要显式启用原生 Hook 测试")
	}
	capturer, err := NewCapturer()
	if err != nil {
		t.Fatal(err)
	}
	defer capturer.Close()
	injector, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer injector.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	counts := make(map[uint32]int)
	original := mouseCallback
	mouseCallback = syscall.NewCallback(func(code, message, data uintptr) uintptr {
		if int32(code) >= 0 && (*mouseHookData)(unsafe.Pointer(data)).Flags&llMouseInjected != 0 {
			counts[uint32(message)]++
			if counts[wmLeftDown] > 0 && counts[wmLeftUp] > 0 && counts[wmRightDown] > 0 && counts[wmRightUp] > 0 {
				cancel()
			}
			return 1
		}
		return capturer.handleMouse(code, uint32(message), data)
	})
	defer func() { mouseCallback = original }()
	sent := make(chan error, 1)
	timer := time.AfterFunc(100*time.Millisecond, func() {
		if ctx.Err() != nil {
			sent <- ctx.Err()
			return
		}
		sent <- injector.send([]nativeInput{
			newMouse(0, 0, 0, mouseLeftDown), newMouse(0, 0, 0, mouseLeftUp),
			newMouse(0, 0, 0, mouseRightDown), newMouse(0, 0, 0, mouseRightUp),
		})
	})
	capturer.Capture(ctx, func(common.Event) error { return nil })
	if timer.Stop() {
		t.Fatal("捕获器在原生输入测试开始前退出")
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	t.Logf("injected_mouse_messages=%v", counts)
	for _, message := range []uint32{wmLeftDown, wmLeftUp, wmRightDown, wmRightUp} {
		if counts[message] != 1 {
			t.Fatalf("未收到原生鼠标消息 %#x: %v", message, counts)
		}
	}
}

func TestPhysicalCapture(t *testing.T) {
	if os.Getenv("MYAPP_WINDOWS_INPUT_TEST") != "1" {
		t.Skip("需要显式启用实体键鼠测试")
	}
	capturer, err := NewCapturer()
	if err != nil {
		t.Fatal(err)
	}
	defer capturer.Close()
	counts := make(map[uint32]int)
	flags := make(map[uint32]int)
	original := mouseCallback
	mouseCallback = syscall.NewCallback(func(code, message, data uintptr) uintptr {
		if int32(code) >= 0 {
			counts[uint32(message)]++
			flags[(*mouseHookData)(unsafe.Pointer(data)).Flags]++
		}
		return capturer.handleMouse(code, uint32(message), data)
	})
	defer func() { mouseCallback = original }()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pressed := make(map[uint16]bool)
	var a, button, emergency bool
	err = capturer.Capture(ctx, func(event common.Event) error {
		if event.Kind == common.KeyDown {
			pressed[event.Code] = true
			a = a || event.Code == 4
			if event.Code == 41 && (pressed[224] || pressed[228]) && (pressed[226] || pressed[230]) {
				emergency = true
				cancel()
			}
		}
		if event.Kind == common.KeyUp {
			delete(pressed, event.Code)
		}
		button = button || event.Kind == common.MouseButtonDown && event.Code == 1
		return nil
	})
	t.Logf("mouse_messages=%v mouse_flags=%v a=%v left_button=%v emergency=%v capture_error=%v", counts, flags, a, button, emergency, err)
	if !a || !button || !emergency {
		t.Fatal("实体键鼠捕获未完整通过")
	}
}
