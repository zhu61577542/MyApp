//go:build windows

package wininput

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	common "myapp/internal/input"
)

const (
	whKeyboardLowLevel = 13
	whMouseLowLevel    = 14
	wmQuit             = 0x0012
	wmKeyDown          = 0x0100
	wmKeyUp            = 0x0101
	wmSysKeyDown       = 0x0104
	wmSysKeyUp         = 0x0105
	wmMouseMove        = 0x0200
	wmLeftDown         = 0x0201
	wmLeftUp           = 0x0202
	wmRightDown        = 0x0204
	wmRightUp          = 0x0205
	wmMiddleDown       = 0x0207
	wmMiddleUp         = 0x0208
	wmMouseWheel       = 0x020a
	wmXDown            = 0x020b
	wmXUp              = 0x020c
	wmMouseHWheel      = 0x020e
	llMouseInjected    = 0x00000001
	llKeyboardInjected = 0x00000010
	llKeyboardExtended = 0x00000001
)

type nativePoint struct {
	X int32
	Y int32
}

type mouseHookData struct {
	Point     nativePoint
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keyboardHookData struct {
	VirtualKey uint32
	ScanCode   uint32
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
}

type nativeMessage struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   nativePoint
	Private uint32
}

var (
	activeCapturer atomic.Pointer[Capturer]
	mouseCallback  = syscall.NewCallback(func(code, message, data uintptr) uintptr {
		capturer := activeCapturer.Load()
		if capturer == nil {
			return 0
		}
		return capturer.handleMouse(code, uint32(message), data)
	})
	keyboardCallback = syscall.NewCallback(func(code, message, data uintptr) uintptr {
		capturer := activeCapturer.Load()
		if capturer == nil {
			return 0
		}
		return capturer.handleKeyboard(code, uint32(message), data)
	})
)

type Capturer struct {
	mu               sync.Mutex
	setHook          *syscall.LazyProc
	unhook           *syscall.LazyProc
	callNext         *syscall.LazyProc
	getMessage       *syscall.LazyProc
	postThread       *syscall.LazyProc
	getCurrentThread *syscall.LazyProc
	threadID         uint32
	handler          func(common.Event) error
	callbackErr      error
	sequence         uint64
	lastPoint        nativePoint
	hasLastPoint     bool
}

func NewCapturer() (*Capturer, error) {
	user32 := syscall.NewLazyDLL("user32.dll")
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	capturer := &Capturer{
		setHook:          user32.NewProc("SetWindowsHookExW"),
		unhook:           user32.NewProc("UnhookWindowsHookEx"),
		callNext:         user32.NewProc("CallNextHookEx"),
		getMessage:       user32.NewProc("GetMessageW"),
		postThread:       user32.NewProc("PostThreadMessageW"),
		getCurrentThread: kernel32.NewProc("GetCurrentThreadId"),
	}
	for _, procedure := range []*syscall.LazyProc{capturer.setHook, capturer.unhook, capturer.callNext, capturer.getMessage, capturer.postThread, capturer.getCurrentThread} {
		if err := procedure.Find(); err != nil {
			return nil, err
		}
	}
	return capturer, nil
}

func (c *Capturer) Capture(ctx context.Context, handler func(common.Event) error) error {
	if handler == nil {
		return errors.New("输入处理函数不能为空")
	}
	if !activeCapturer.CompareAndSwap(nil, c) {
		return errors.New("Windows 输入捕获器已在运行")
	}
	defer activeCapturer.CompareAndSwap(c, nil)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	threadID, _, _ := c.getCurrentThread.Call()
	c.mu.Lock()
	c.threadID = uint32(threadID)
	c.handler, c.callbackErr, c.sequence, c.hasLastPoint = handler, nil, 1, false
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.threadID, c.handler = 0, nil
		c.mu.Unlock()
	}()
	mouseHook, _, mouseErr := c.setHook.Call(whMouseLowLevel, mouseCallback, 0, 0)
	if mouseHook == 0 {
		return nativeCallError(mouseErr, "无法安装 Windows 鼠标 Hook")
	}
	defer c.unhook.Call(mouseHook)
	keyboardHook, _, keyboardErr := c.setHook.Call(whKeyboardLowLevel, keyboardCallback, 0, 0)
	if keyboardHook == 0 {
		return nativeCallError(keyboardErr, "无法安装 Windows 键盘 Hook")
	}
	defer c.unhook.Call(keyboardHook)
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.stop()
		case <-stopped:
		}
	}()
	var message nativeMessage
	for {
		result, _, callErr := c.getMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) == -1 {
			close(stopped)
			return nativeCallError(callErr, "Windows 消息循环失败")
		}
		if result == 0 {
			break
		}
	}
	close(stopped)
	c.mu.Lock()
	callbackErr := c.callbackErr
	c.mu.Unlock()
	if callbackErr != nil {
		return callbackErr
	}
	return ctx.Err()
}

func (c *Capturer) Close() error {
	c.stop()
	return nil
}

func (c *Capturer) stop() {
	c.mu.Lock()
	threadID := c.threadID
	c.mu.Unlock()
	if threadID != 0 {
		c.postThread.Call(uintptr(threadID), wmQuit, 0, 0)
	}
}

func (c *Capturer) handleMouse(code uintptr, message uint32, data uintptr) uintptr {
	if int32(code) < 0 {
		return c.next(code, uintptr(message), data)
	}
	native := (*mouseHookData)(unsafe.Pointer(data))
	if native.Flags&llMouseInjected != 0 {
		return c.next(code, uintptr(message), data)
	}
	c.mu.Lock()
	event, emit := c.mouseEventLocked(message, native)
	c.mu.Unlock()
	if emit {
		c.emit(event)
	}
	return 1
}

func (c *Capturer) mouseEventLocked(message uint32, native *mouseHookData) (common.Event, bool) {
	event := common.Event{Sequence: c.sequence}
	switch message {
	case wmMouseMove:
		if !c.hasLastPoint {
			c.lastPoint, c.hasLastPoint = native.Point, true
			return common.Event{}, false
		}
		event.Kind = common.MouseRelative
		event.X, event.Y = native.Point.X-c.lastPoint.X, native.Point.Y-c.lastPoint.Y
		c.lastPoint = native.Point
		if event.X == 0 && event.Y == 0 {
			return common.Event{}, false
		}
	case wmLeftDown, wmLeftUp, wmRightDown, wmRightUp, wmMiddleDown, wmMiddleUp, wmXDown, wmXUp:
		event.Kind = common.MouseButtonDown
		if message == wmLeftUp || message == wmRightUp || message == wmMiddleUp || message == wmXUp {
			event.Kind = common.MouseButtonUp
		}
		switch message {
		case wmLeftDown, wmLeftUp:
			event.Code = 1
		case wmRightDown, wmRightUp:
			event.Code = 2
		case wmMiddleDown, wmMiddleUp:
			event.Code = 3
		default:
			event.Code = uint16(native.MouseData>>16) + 3
		}
	case wmMouseWheel, wmMouseHWheel:
		delta := int32(int16(native.MouseData>>16)) / 120
		if delta == 0 {
			return common.Event{}, false
		}
		event.Kind = common.MouseScroll
		if message == wmMouseWheel {
			event.Y = delta
		} else {
			event.X = delta
		}
	default:
		return common.Event{}, false
	}
	c.sequence++
	return event, true
}

func (c *Capturer) handleKeyboard(code uintptr, message uint32, data uintptr) uintptr {
	if int32(code) < 0 {
		return c.next(code, uintptr(message), data)
	}
	native := (*keyboardHookData)(unsafe.Pointer(data))
	if native.Flags&llKeyboardInjected != 0 {
		return c.next(code, uintptr(message), data)
	}
	extended := native.Flags&llKeyboardExtended != 0
	hid, ok := scanCodeToHID[scanCodeEntry{scan: uint16(native.ScanCode), extended: extended}]
	if !ok {
		return 1
	}
	kind := common.KeyDown
	if message == wmKeyUp || message == wmSysKeyUp {
		kind = common.KeyUp
	} else if message != wmKeyDown && message != wmSysKeyDown {
		return 1
	}
	c.mu.Lock()
	event := common.Event{Kind: kind, Sequence: c.sequence, Code: hid}
	c.sequence++
	c.mu.Unlock()
	c.emit(event)
	return 1
}

func (c *Capturer) emit(event common.Event) {
	if err := event.Validate(); err != nil {
		c.fail(err)
		return
	}
	c.mu.Lock()
	handler := c.handler
	c.mu.Unlock()
	if handler != nil {
		if err := handler(event); err != nil {
			c.fail(err)
		}
	}
}

func (c *Capturer) fail(err error) {
	c.mu.Lock()
	if c.callbackErr == nil {
		c.callbackErr = err
	}
	c.mu.Unlock()
	c.stop()
}

func (c *Capturer) next(code, message, data uintptr) uintptr {
	result, _, _ := c.callNext.Call(0, code, message, data)
	return result
}

func nativeCallError(err error, fallback string) error {
	if err != nil && err != syscall.Errno(0) {
		return err
	}
	return errors.New(fallback)
}

var scanCodeToHID = func() map[scanCodeEntry]uint16 {
	result := make(map[scanCodeEntry]uint16, len(windowsScanCodes))
	for hid, entry := range windowsScanCodes {
		result[entry] = hid
	}
	return result
}()

var _ common.Capturer = (*Capturer)(nil)
