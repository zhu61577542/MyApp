//go:build windows

package wininput

import (
	"errors"
	common "myapp/internal/input"
	"syscall"
	"unsafe"
)

const wmInput = 0x00ff

type rawDevice struct {
	Page, Usage uint16
	Flags       uint32
	Window      uintptr
}
type rawHeader struct {
	Type, Size    uint32
	Device, Param uintptr
}
type rawMouse struct {
	Flags, Padding      uint16
	Buttons, RawButtons uint32
	X, Y                int32
	Extra               uint32
}
type rawCapture struct {
	create, destroy, register, data, dispatch, dpi, metrics *syscall.LazyProc
	window                                                  uintptr
	absX, absY                                              int32
	hasAbsolute                                             bool
}

func newRawCapture() (*rawCapture, error) {
	u := syscall.NewLazyDLL("user32.dll")
	r := &rawCapture{create: u.NewProc("CreateWindowExW"), destroy: u.NewProc("DestroyWindow"), register: u.NewProc("RegisterRawInputDevices"), data: u.NewProc("GetRawInputData"), dispatch: u.NewProc("DispatchMessageW"), dpi: u.NewProc("SetThreadDpiAwarenessContext"), metrics: u.NewProc("GetSystemMetrics")}
	for _, proc := range []*syscall.LazyProc{r.create, r.destroy, r.register, r.data, r.dispatch, r.dpi, r.metrics} {
		if err := proc.Find(); err != nil {
			return nil, err
		}
	}
	return r, nil
}
func (r *rawCapture) start() error {
	class, _ := syscall.UTF16PtrFromString("STATIC")
	window, _, err := r.create.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, ^uintptr(2), 0, 0, 0)
	if window == 0 {
		return nativeCallError(err, "无法创建原始输入消息窗口")
	}
	r.window = window
	r.hasAbsolute = false
	device := rawDevice{Page: 1, Usage: 2, Flags: 0x100, Window: window}
	ok, _, err := r.register.Call(uintptr(unsafe.Pointer(&device)), 1, unsafe.Sizeof(device))
	if ok == 0 {
		r.destroy.Call(window)
		r.window = 0
		return nativeCallError(err, "无法登记原始鼠标输入")
	}
	return nil
}
func (r *rawCapture) close() {
	if r.window == 0 {
		return
	}
	device := rawDevice{Page: 1, Usage: 2, Flags: 1}
	r.register.Call(uintptr(unsafe.Pointer(&device)), 1, unsafe.Sizeof(device))
	r.destroy.Call(r.window)
	r.window = 0
}
func (r *rawCapture) handle(c *Capturer, handle uintptr) error {
	var size uint32
	result, _, err := r.data.Call(handle, 0x10000003, 0, uintptr(unsafe.Pointer(&size)), unsafe.Sizeof(rawHeader{}))
	if int32(result) == -1 {
		return nativeCallError(err, "无法读取原始鼠标输入长度")
	}
	if size < uint32(unsafe.Sizeof(rawHeader{}))+uint32(unsafe.Sizeof(rawMouse{})) || size > 4096 {
		return errors.New("原始鼠标输入长度无效")
	}
	buffer := make([]byte, size)
	result, _, err = r.data.Call(handle, 0x10000003, uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), unsafe.Sizeof(rawHeader{}))
	if int32(result) == -1 {
		return nativeCallError(err, "无法读取原始鼠标输入")
	}
	header := (*rawHeader)(unsafe.Pointer(&buffer[0]))
	if header.Type != 0 {
		return nil
	}
	c.rawSeen.Add(1)
	mouse := (*rawMouse)(unsafe.Pointer(&buffer[unsafe.Sizeof(rawHeader{})]))
	dx, dy := mouse.X, mouse.Y
	if mouse.Flags&1 != 0 {
		if !r.hasAbsolute {
			r.absX = dx
			r.absY = dy
			r.hasAbsolute = true
			return nil
		}
		wx, hy := uintptr(0), uintptr(1)
		if mouse.Flags&2 != 0 {
			wx = 78
			hy = 79
		}
		width, _, _ := r.metrics.Call(wx)
		height, _, _ := r.metrics.Call(hy)
		dx = int32(int64(mouse.X-r.absX) * int64(width) / 65535)
		dy = int32(int64(mouse.Y-r.absY) * int64(height) / 65535)
		r.absX = mouse.X
		r.absY = mouse.Y
	}
	if dx == 0 && dy == 0 {
		return nil
	}
	c.mu.Lock()
	event := common.Event{Kind: common.MouseRelative, Sequence: c.sequence, X: dx, Y: dy}
	c.sequence++
	c.mu.Unlock()
	c.emit(event)
	return nil
}
