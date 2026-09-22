//go:build darwin

package darwininput

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
	common "myapp/internal/input"
)

const (
	tapDisabledTimeout = ^uint32(1)
	tapDisabledUser    = ^uint32(0)
)

var (
	capturerRegistry sync.Map
	capturerToken    atomic.Uint64
	eventTapCallback = purego.NewCallback(func(_ uintptr, eventType uint32, event uintptr, token uintptr) uintptr {
		value, exists := capturerRegistry.Load(token)
		if !exists {
			return event
		}
		return value.(*Capturer).handleEvent(eventType, event)
	})
)

type Capturer struct {
	captureMu       sync.Mutex
	stateMu         sync.Mutex
	createTap       func(uint32, uint32, uint32, uint64, uintptr, uintptr) uintptr
	enableTap       func(uintptr, bool)
	preflightListen func() bool
	getInteger      func(uintptr, uint32) int64
	makeSource      func(uintptr, uintptr, int64) uintptr
	getRunLoop      func() uintptr
	addSource       func(uintptr, uintptr, uintptr)
	removeSource    func(uintptr, uintptr, uintptr)
	runLoopRun      func()
	runLoopStop     func(uintptr)
	release         func(uintptr)
	commonModes     uintptr
	tap             uintptr
	runLoop         uintptr
	handler         func(common.Event) error
	callbackErr     error
	sequence        uint64
	modifierDown    map[uint16]bool
}

func NewCapturer() (*Capturer, error) {
	coreGraphics, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	coreFoundation, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	capturer := &Capturer{modifierDown: make(map[uint16]bool)}
	purego.RegisterLibFunc(&capturer.createTap, coreGraphics, "CGEventTapCreate")
	purego.RegisterLibFunc(&capturer.enableTap, coreGraphics, "CGEventTapEnable")
	purego.RegisterLibFunc(&capturer.preflightListen, coreGraphics, "CGPreflightListenEventAccess")
	purego.RegisterLibFunc(&capturer.getInteger, coreGraphics, "CGEventGetIntegerValueField")
	purego.RegisterLibFunc(&capturer.makeSource, coreFoundation, "CFMachPortCreateRunLoopSource")
	purego.RegisterLibFunc(&capturer.getRunLoop, coreFoundation, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&capturer.addSource, coreFoundation, "CFRunLoopAddSource")
	purego.RegisterLibFunc(&capturer.removeSource, coreFoundation, "CFRunLoopRemoveSource")
	purego.RegisterLibFunc(&capturer.runLoopRun, coreFoundation, "CFRunLoopRun")
	purego.RegisterLibFunc(&capturer.runLoopStop, coreFoundation, "CFRunLoopStop")
	purego.RegisterLibFunc(&capturer.release, coreFoundation, "CFRelease")
	modeSymbol, err := purego.Dlsym(coreFoundation, "kCFRunLoopCommonModes")
	if err != nil {
		return nil, err
	}
	capturer.commonModes = *(*uintptr)(unsafe.Pointer(modeSymbol))
	if capturer.commonModes == 0 {
		return nil, errors.New("无法读取 kCFRunLoopCommonModes")
	}
	return capturer, nil
}

func (c *Capturer) CanCapture() bool {
	return c.preflightListen()
}

func (c *Capturer) Capture(ctx context.Context, handler func(common.Event) error) error {
	if handler == nil {
		return errors.New("输入处理函数不能为空")
	}
	if !c.CanCapture() {
		return errors.New("macOS 输入监控权限未授权")
	}
	c.captureMu.Lock()
	defer c.captureMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	mask := eventMask(1, 2, 3, 4, 5, 6, 7, 10, 11, 12, 22, 25, 26, 27)
	token := uintptr(capturerToken.Add(1))
	capturerRegistry.Store(token, c)
	defer capturerRegistry.Delete(token)
	tap := c.createTap(0, 0, 0, mask, eventTapCallback, token)
	if tap == 0 {
		return errors.New("CGEventTapCreate 失败")
	}
	defer c.release(tap)
	source := c.makeSource(0, tap, 0)
	if source == 0 {
		return errors.New("无法创建 Event Tap RunLoop source")
	}
	defer c.release(source)
	runLoop := c.getRunLoop()
	if runLoop == 0 {
		return errors.New("无法取得当前 CFRunLoop")
	}
	c.stateMu.Lock()
	c.tap, c.runLoop, c.handler, c.callbackErr, c.sequence = tap, runLoop, handler, nil, 1
	clear(c.modifierDown)
	c.stateMu.Unlock()
	defer func() {
		c.stateMu.Lock()
		c.tap, c.runLoop, c.handler = 0, 0, nil
		c.stateMu.Unlock()
	}()
	c.addSource(runLoop, source, c.commonModes)
	defer c.removeSource(runLoop, source, c.commonModes)
	c.enableTap(tap, true)
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.runLoopStop(runLoop)
		case <-stopped:
		}
	}()
	c.runLoopRun()
	close(stopped)
	c.stateMu.Lock()
	callbackErr := c.callbackErr
	c.stateMu.Unlock()
	if callbackErr != nil {
		return callbackErr
	}
	return ctx.Err()
}

func (c *Capturer) Close() error {
	c.stateMu.Lock()
	runLoop := c.runLoop
	c.stateMu.Unlock()
	if runLoop != 0 {
		c.runLoopStop(runLoop)
	}
	return nil
}

func (c *Capturer) handleEvent(eventType uint32, event uintptr) uintptr {
	c.stateMu.Lock()
	if eventType == tapDisabledTimeout || eventType == tapDisabledUser {
		if c.tap != 0 {
			c.enableTap(c.tap, true)
		}
		c.stateMu.Unlock()
		return event
	}
	handler, sequence := c.handler, c.sequence
	inputEvent, emit := c.translateLocked(eventType, event, sequence)
	if emit {
		c.sequence++
	}
	c.stateMu.Unlock()
	if !emit || handler == nil {
		return 0
	}
	if err := handler(inputEvent); err != nil {
		c.stateMu.Lock()
		c.callbackErr = err
		runLoop := c.runLoop
		c.stateMu.Unlock()
		if runLoop != 0 {
			c.runLoopStop(runLoop)
		}
	}
	return 0
}

func (c *Capturer) translateLocked(eventType uint32, event uintptr, sequence uint64) (common.Event, bool) {
	switch eventType {
	case 5, 6, 7, 27:
		dx := int32(c.getInteger(event, 4))
		dy := int32(c.getInteger(event, 5))
		if dx == 0 && dy == 0 {
			return common.Event{}, false
		}
		return common.Event{Kind: common.MouseRelative, Sequence: sequence, X: dx, Y: dy}, true
	case 1, 2, 3, 4, 25, 26:
		button := uint16(c.getInteger(event, 3)) + 1
		if button == 2 {
			button = 2
		}
		kind := common.MouseButtonDown
		if eventType == 2 || eventType == 4 || eventType == 26 {
			kind = common.MouseButtonUp
		}
		return common.Event{Kind: kind, Sequence: sequence, Code: button}, button >= 1 && button <= 5
	case 22:
		vertical := int32(c.getInteger(event, 11))
		horizontal := int32(c.getInteger(event, 12))
		if vertical == 0 && horizontal == 0 {
			return common.Event{}, false
		}
		return common.Event{Kind: common.MouseScroll, Sequence: sequence, X: horizontal, Y: vertical}, true
	case 10, 11:
		macCode := uint16(c.getInteger(event, 9))
		hid, ok := macCodeToHID[macCode]
		if !ok {
			return common.Event{}, false
		}
		kind := common.KeyDown
		if eventType == 11 {
			kind = common.KeyUp
		}
		return common.Event{Kind: kind, Sequence: sequence, Code: hid}, true
	case 12:
		macCode := uint16(c.getInteger(event, 9))
		hid, ok := macCodeToHID[macCode]
		if !ok {
			return common.Event{}, false
		}
		down := !c.modifierDown[macCode]
		c.modifierDown[macCode] = down
		kind := common.KeyDown
		if !down {
			kind = common.KeyUp
		}
		return common.Event{Kind: kind, Sequence: sequence, Code: hid}, true
	default:
		return common.Event{}, false
	}
}

var macCodeToHID = func() map[uint16]uint16 {
	result := make(map[uint16]uint16, len(macKeyCodes))
	for hid, macCode := range macKeyCodes {
		result[macCode] = hid
	}
	return result
}()

func eventMask(eventTypes ...uint32) uint64 {
	var mask uint64
	for _, eventType := range eventTypes {
		if eventType < 64 {
			mask |= uint64(1) << eventType
		}
	}
	return mask
}
