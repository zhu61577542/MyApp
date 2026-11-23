//go:build windows

package pointer

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

type guardClass struct {
	Size, Style                   uint32
	Procedure                     uintptr
	ClassExtra, WindowExtra       int32
	Instance, Icon, Cursor, Brush uintptr
	Menu, Name                    *uint16
	SmallIcon                     uintptr
}
type guardMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          nativePoint
	Private        uint32
}

// 微小非激活窗口持有指针显示状态；管道关闭或进程退出后由系统销毁。
func runWindowGuard(input io.Reader, output io.Writer) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := syscall.NewLazyDLL("user32.dll")
	register, unregister, create, destroy := u.NewProc("RegisterClassExW"), u.NewProc("UnregisterClassW"), u.NewProc("CreateWindowExW"), u.NewProc("DestroyWindow")
	def, setCursor, showCursor := u.NewProc("DefWindowProcW"), u.NewProc("SetCursor"), u.NewProc("ShowCursor")
	getMessage, dispatch, post, quit := u.NewProc("GetMessageW"), u.NewProc("DispatchMessageW"), u.NewProc("PostMessageW"), u.NewProc("PostQuitMessage")
	layer, position, timer := u.NewProc("SetLayeredWindowAttributes"), u.NewProc("SetWindowPos"), u.NewProc("SetTimer")
	for _, p := range []*syscall.LazyProc{register, unregister, create, destroy, def, setCursor, showCursor, getMessage, dispatch, post, quit, layer, position, timer} {
		if err := p.Find(); err != nil {
			return err
		}
	}
	backend, e := New()
	if e != nil {
		return e
	}
	p := backend.(*nativeBackend)
	previous, _, _ := p.dpi.Call(^uintptr(3))
	if previous != 0 {
		defer p.dpi.Call(previous)
	}
	point, e := p.Position()
	if e != nil {
		return e
	}
	module, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	name, _ := syscall.UTF16PtrFromString("MyAppCursorLease")
	var callbackErr error
	callback := syscall.NewCallback(func(window uintptr, message uint32, wparam, lparam uintptr) uintptr {
		switch message {
		case 0x0084:
			return 1
		case 0x0020:
			setCursor.Call(0)
			return 1
		case 0x0021:
			return 4
		case 0x0010:
			destroy.Call(window)
			return 0
		case 0x0002:
			quit.Call(0)
			return 0
		}
		result, _, _ := def.Call(window, uintptr(message), wparam, lparam)
		return result
	})
	class := guardClass{Size: uint32(unsafe.Sizeof(guardClass{})), Procedure: callback, Instance: module, Name: name}
	atom, _, err := register.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return fmt.Errorf("登记光标看护窗口失败: %v", err)
	}
	defer unregister.Call(uintptr(unsafe.Pointer(name)), module)
	window, _, err := create.Call(0x08080088, uintptr(unsafe.Pointer(name)), 0, 0x80000000, uintptr(int32(point.X-2)), uintptr(int32(point.Y-2)), 5, 5, 0, 0, module, 0)
	if window == 0 {
		return fmt.Errorf("创建光标看护窗口失败: %v", err)
	}
	defer destroy.Call(window)
	if ok, _, err := layer.Call(window, 0, 1, 2); ok == 0 {
		return fmt.Errorf("设置光标看护窗口失败: %v", err)
	}
	move := func() error {
		point, err := p.Position()
		if err != nil {
			return err
		}
		if ok, _, err := position.Call(window, ^uintptr(0), uintptr(int32(point.X-2)), uintptr(int32(point.Y-2)), 5, 5, 0x0050); ok == 0 {
			return fmt.Errorf("定位光标看护窗口失败: %v", err)
		}
		setCursor.Call(0)
		return nil
	}
	if err := move(); err != nil {
		return err
	}
	decrements := 0
	for decrements < 32 {
		count, _, _ := showCursor.Call(0)
		decrements++
		if int32(count) < 0 {
			break
		}
	}
	defer func() {
		for range decrements {
			showCursor.Call(1)
		}
	}()
	// 同坐标刷新命中归属，不产生可见移动。
	if err := p.Warp(point); err != nil {
		return err
	}
	if ok, _, err := timer.Call(window, 1, 50, 0); ok == 0 {
		return fmt.Errorf("启动光标看护计时器失败: %v", err)
	}
	go func() { _, _ = io.Copy(io.Discard, input); post.Call(window, 0x0010, 0, 0) }()
	ready := false
	deadline := time.Now().Add(1500 * time.Millisecond)
	var message guardMessage
	for {
		result, _, err := getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("光标看护消息循环失败: %v", err)
		}
		if result == 0 {
			break
		}
		if message.Message == 0x0113 {
			if err := move(); err != nil {
				callbackErr = err
				break
			}
			state := cursorInfo{Size: uint32(unsafe.Sizeof(cursorInfo{}))}
			if ok, _, err := u.NewProc("GetCursorInfo").Call(uintptr(unsafe.Pointer(&state))); ok == 0 {
				callbackErr = fmt.Errorf("读取实际光标状态失败: %v", err)
				break
			}
			if !ready {
				if state.Flags&1 == 0 {
					if _, err := fmt.Fprintln(output, "hidden"); err != nil {
						callbackErr = err
						break
					}
					ready = true
				} else if time.Now().After(deadline) {
					callbackErr = errors.New("当前应用仍持有光标，未能确认隐藏")
					break
				}
			}
		}
		dispatch.Call(uintptr(unsafe.Pointer(&message)))
	}
	return callbackErr
}
