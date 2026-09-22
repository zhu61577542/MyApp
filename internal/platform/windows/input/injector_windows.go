//go:build windows

package wininput

import (
	"errors"
	"fmt"
	"math"
	"syscall"
	"unsafe"

	common "myapp/internal/input"
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove        = 0x0001
	mouseLeftDown    = 0x0002
	mouseLeftUp      = 0x0004
	mouseRightDown   = 0x0008
	mouseRightUp     = 0x0010
	mouseMiddleDown  = 0x0020
	mouseMiddleUp    = 0x0040
	mouseXDown       = 0x0080
	mouseXUp         = 0x0100
	mouseWheel       = 0x0800
	mouseHorizontal  = 0x1000
	mouseVirtualDesk = 0x4000
	mouseAbsolute    = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyScanCode = 0x0008
)

type nativeInput struct {
	Type    uint32
	Padding uint32
	Data    [32]byte
}

type mouseInputData struct {
	DX        int32
	DY        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type keyboardInputData struct {
	VirtualKey uint16
	ScanCode   uint16
	Flags      uint32
	Time       uint32
	ExtraInfo  uintptr
}

type Injector struct {
	sendInput        *syscall.LazyProc
	getSystemMetrics *syscall.LazyProc
	tracker          *common.Tracker
}

func New() (*Injector, error) {
	user32 := syscall.NewLazyDLL("user32.dll")
	sendInput := user32.NewProc("SendInput")
	getSystemMetrics := user32.NewProc("GetSystemMetrics")
	if err := sendInput.Find(); err != nil {
		return nil, err
	}
	if err := getSystemMetrics.Find(); err != nil {
		return nil, err
	}
	return &Injector{sendInput: sendInput, getSystemMetrics: getSystemMetrics, tracker: common.NewTracker()}, nil
}

func (i *Injector) Inject(event common.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if event.Kind == common.ReleaseAll {
		return i.ReleaseAll()
	}
	inputs, err := i.translate(event)
	if err != nil {
		return err
	}
	if err := i.send(inputs); err != nil {
		return err
	}
	i.tracker.Apply(event)
	return nil
}

func (i *Injector) ReleaseAll() error {
	events, _ := i.tracker.ReleaseEvents(1)
	var result error
	for _, event := range events {
		if err := i.Inject(event); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (i *Injector) Close() error {
	return i.ReleaseAll()
}

func (i *Injector) translate(event common.Event) ([]nativeInput, error) {
	switch event.Kind {
	case common.MouseAbsolute:
		left, top, width, height := i.virtualDesktop()
		if width < 2 || height < 2 {
			return nil, errors.New("无法读取 Windows 虚拟桌面尺寸")
		}
		dx := clamp64(int64(event.X-left)*65535/int64(width-1), 0, 65535)
		dy := clamp64(int64(event.Y-top)*65535/int64(height-1), 0, 65535)
		return []nativeInput{newMouse(int32(dx), int32(dy), 0, mouseMove|mouseAbsolute|mouseVirtualDesk)}, nil
	case common.MouseRelative:
		return []nativeInput{newMouse(event.X, event.Y, 0, mouseMove)}, nil
	case common.MouseButtonDown, common.MouseButtonUp:
		flags, data, err := mouseButton(event.Code, event.Kind == common.MouseButtonDown)
		if err != nil {
			return nil, err
		}
		return []nativeInput{newMouse(0, 0, data, flags)}, nil
	case common.MouseScroll:
		var inputs []nativeInput
		if event.Y != 0 {
			inputs = append(inputs, newMouse(0, 0, uint32(event.Y*120), mouseWheel))
		}
		if event.X != 0 {
			inputs = append(inputs, newMouse(0, 0, uint32(event.X*120), mouseHorizontal))
		}
		return inputs, nil
	case common.KeyDown, common.KeyUp:
		scanCode, extended, ok := windowsScanCode(event.Code)
		if !ok {
			return nil, fmt.Errorf("Windows 暂不支持 HID 键码 %d", event.Code)
		}
		flags := uint32(keyScanCode)
		if extended {
			flags |= keyExtended
		}
		if event.Kind == common.KeyUp {
			flags |= keyUp
		}
		return []nativeInput{newKeyboard(scanCode, flags)}, nil
	default:
		return nil, errors.New("Windows 不支持该输入事件")
	}
}

func (i *Injector) send(inputs []nativeInput) error {
	if len(inputs) == 0 {
		return nil
	}
	written, _, callErr := i.sendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(nativeInput{}))
	if int(written) != len(inputs) {
		if callErr != syscall.Errno(0) {
			return callErr
		}
		return fmt.Errorf("SendInput 只发送了 %d/%d 个事件", written, len(inputs))
	}
	return nil
}

func (i *Injector) virtualDesktop() (left, top, width, height int32) {
	metric := func(index uintptr) int32 {
		value, _, _ := i.getSystemMetrics.Call(index)
		return int32(value)
	}
	return metric(76), metric(77), metric(78), metric(79)
}

func newMouse(dx, dy int32, data, flags uint32) nativeInput {
	result := nativeInput{Type: inputMouse}
	*(*mouseInputData)(unsafe.Pointer(&result.Data[0])) = mouseInputData{DX: dx, DY: dy, MouseData: data, Flags: flags}
	return result
}

func newKeyboard(scanCode uint16, flags uint32) nativeInput {
	result := nativeInput{Type: inputKeyboard}
	*(*keyboardInputData)(unsafe.Pointer(&result.Data[0])) = keyboardInputData{ScanCode: scanCode, Flags: flags}
	return result
}

func mouseButton(code uint16, down bool) (flags, data uint32, err error) {
	switch code {
	case 1:
		if down {
			return mouseLeftDown, 0, nil
		}
		return mouseLeftUp, 0, nil
	case 2:
		if down {
			return mouseRightDown, 0, nil
		}
		return mouseRightUp, 0, nil
	case 3:
		if down {
			return mouseMiddleDown, 0, nil
		}
		return mouseMiddleUp, 0, nil
	case 4, 5:
		data = uint32(code - 3)
		if down {
			return mouseXDown, data, nil
		}
		return mouseXUp, data, nil
	default:
		return 0, 0, errors.New("Windows 鼠标按钮无效")
	}
}

func windowsScanCode(hid uint16) (uint16, bool, bool) {
	entry, ok := windowsScanCodes[hid]
	return entry.scan, entry.extended, ok
}

type scanCodeEntry struct {
	scan     uint16
	extended bool
}

var windowsScanCodes = map[uint16]scanCodeEntry{
	4: {0x1e, false}, 5: {0x30, false}, 6: {0x2e, false}, 7: {0x20, false}, 8: {0x12, false}, 9: {0x21, false}, 10: {0x22, false}, 11: {0x23, false}, 12: {0x17, false}, 13: {0x24, false}, 14: {0x25, false}, 15: {0x26, false}, 16: {0x32, false}, 17: {0x31, false}, 18: {0x18, false}, 19: {0x19, false}, 20: {0x10, false}, 21: {0x13, false}, 22: {0x1f, false}, 23: {0x14, false}, 24: {0x16, false}, 25: {0x2f, false}, 26: {0x11, false}, 27: {0x2d, false}, 28: {0x15, false}, 29: {0x2c, false},
	30: {0x02, false}, 31: {0x03, false}, 32: {0x04, false}, 33: {0x05, false}, 34: {0x06, false}, 35: {0x07, false}, 36: {0x08, false}, 37: {0x09, false}, 38: {0x0a, false}, 39: {0x0b, false}, 40: {0x1c, false}, 41: {0x01, false}, 42: {0x0e, false}, 43: {0x0f, false}, 44: {0x39, false}, 45: {0x0c, false}, 46: {0x0d, false}, 47: {0x1a, false}, 48: {0x1b, false}, 49: {0x2b, false}, 51: {0x27, false}, 52: {0x28, false}, 53: {0x29, false}, 54: {0x33, false}, 55: {0x34, false}, 56: {0x35, false}, 57: {0x3a, false},
	58: {0x3b, false}, 59: {0x3c, false}, 60: {0x3d, false}, 61: {0x3e, false}, 62: {0x3f, false}, 63: {0x40, false}, 64: {0x41, false}, 65: {0x42, false}, 66: {0x43, false}, 67: {0x44, false}, 68: {0x57, false}, 69: {0x58, false}, 73: {0x52, true}, 74: {0x47, true}, 75: {0x49, true}, 76: {0x53, true}, 77: {0x4f, true}, 78: {0x51, true}, 79: {0x4d, true}, 80: {0x4b, true}, 81: {0x50, true}, 82: {0x48, true}, 83: {0x45, false},
	224: {0x1d, false}, 225: {0x2a, false}, 226: {0x38, false}, 227: {0x5b, true}, 228: {0x1d, true}, 229: {0x36, false}, 230: {0x38, true}, 231: {0x5c, true},
}

func clamp64(value, minimum, maximum int64) int64 {
	return int64(math.Max(float64(minimum), math.Min(float64(maximum), float64(value))))
}

var _ common.Injector = (*Injector)(nil)
