//go:build windows

package pointer

import (
	"os"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// 仅查询原生位置，不安装Hook，不注入或移动光标。
func TestWindowsNativePositionMatchesPMv2FromUnawareThread(t *testing.T) {
	if os.Getenv("MYAPP_WINDOWS_READONLY_POINTER_TEST") != "1" {
		t.Skip("需显式启用只读原生坐标检查")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	u := syscall.NewLazyDLL("user32.dll")
	dpi := u.NewProc("SetThreadDpiAwarenessContext")
	read := u.NewProc("GetPhysicalCursorPos")
	original, _, _ := dpi.Call(^uintptr(0))
	if original == 0 {
		t.Fatal("不能建立DPI-unaware测试上下文")
	}
	defer dpi.Call(original)
	backend, err := New()
	if err != nil {
		t.Fatal(err)
	}
	physical := func() nativePoint {
		t.Helper()
		previous, _, _ := dpi.Call(^uintptr(3))
		if previous == 0 {
			t.Fatal("不能读取PMv2参考点")
		}
		defer dpi.Call(previous)
		var point nativePoint
		if ok, _, _ := read.Call(uintptr(unsafe.Pointer(&point))); ok == 0 {
			t.Fatal("读取参考点失败")
		}
		return point
	}
	for range 5 {
		before := physical()
		got, err := backend.Position()
		if err != nil {
			t.Fatal(err)
		}
		after := physical()
		if before != after {
			continue
		}
		if got.X != int(before.X) || got.Y != int(before.Y) {
			t.Fatal("DPI-unaware线程读取的坐标与PMv2物理几何不一致")
		}
		t.Log("DPI-unaware调用已返回与PMv2一致的物理坐标；未操作光标")
		return
	}
	t.Fatal("光标持续移动，未能完成静态坐标对照")
}
