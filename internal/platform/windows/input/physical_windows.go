//go:build windows

package wininput

import (
	common "myapp/internal/input"
	"syscall"
)

func (c *Capturer) physicalMotion(native *mouseHookData) (common.Event, bool) {
	point := native.Point
	previous, known := c.lastPoint, c.hasLastPoint
	c.lastPoint, c.hasLastPoint = native.Point, true
	if common.ConsumeCursorWarp(native.Point.X, native.Point.Y) || native.Flags&llMouseInjected != 0 {
		return common.Event{}, false
	}
	c.physicalPoint.Store(&point)
	if !known {
		return common.Event{}, false
	}
	return common.Event{Kind: common.MouseRelative, X: native.Point.X - previous.X, Y: native.Point.Y - previous.Y}, true
}

func (c *Capturer) PhysicalPosition() (int32, int32, bool) {
	point := c.physicalPoint.Load()
	if point == nil {
		return 0, 0, false
	}
	return point.X, point.Y, true
}

func physicalMouseEvent(message uint32, native *mouseHookData) (common.Event, bool) {
	if native.Flags&llMouseInjected != 0 {
		return common.Event{}, false
	}
	if message == wmMouseMove {
		return common.Event{Kind: common.MouseAbsolute, X: native.Point.X, Y: native.Point.Y}, true
	}
	var translator Capturer
	return translator.mouseEventLocked(message, native)
}

func physicalKeyboardEvent(message uint32, native *keyboardHookData) (common.Event, bool) {
	if native.Flags&llKeyboardInjected != 0 {
		return common.Event{}, false
	}
	kind := common.KeyDown
	switch message {
	case wmKeyDown, wmSysKeyDown:
	case wmKeyUp, wmSysKeyUp:
		kind = common.KeyUp
	default:
		return common.Event{}, false
	}
	hid := scanCodeToHID[scanCodeEntry{scan: uint16(native.ScanCode), extended: native.Flags&llKeyboardExtended != 0}]
	return common.Event{Kind: kind, Code: hid}, true
}

func seedWindowsPhysicalState(activity func(common.Event)) {
	u := syscall.NewLazyDLL("user32.dll")
	state, mapping := u.NewProc("GetAsyncKeyState"), u.NewProc("MapVirtualKeyW")
	keys := make(map[uintptr][]uint16)
	for hid, entry := range windowsScanCodes {
		scan := uintptr(entry.scan)
		if entry.extended {
			scan |= 0xe000
		}
		vk, _, _ := mapping.Call(scan, 3)
		if vk != 0 {
			keys[vk] = append(keys[vk], hid)
		}
	}
	for vk, codes := range keys {
		if len(codes) != 1 {
			continue
		}
		down, _, _ := state.Call(vk)
		if down&0x8000 != 0 {
			activity(common.Event{Kind: common.KeyDown, Code: codes[0]})
		}
	}
	for index, vk := range []uintptr{1, 2, 4, 5, 6} {
		down, _, _ := state.Call(vk)
		if down&0x8000 != 0 {
			activity(common.Event{Kind: common.MouseButtonDown, Code: uint16(index + 1)})
		}
	}
}
