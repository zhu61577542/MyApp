//go:build darwin

package darwininput

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
	common "myapp/internal/input"
)

type point struct {
	X float64
	Y float64
}

type Injector struct {
	mu                 sync.Mutex
	createMouse        func(uintptr, uint32, point, uint32) uintptr
	createKeyboard     func(uintptr, uint16, bool) uintptr
	createScroll       func(uintptr, uint32, uint32, int32, int32, int32) uintptr
	createEvent        func(uintptr) uintptr
	getLocation        func(uintptr) point
	post               func(uint32, uintptr)
	release            func(uintptr)
	preflightPost      func() bool
	tracker            *common.Tracker
	pressedMouseButton map[uint16]struct{}
}

func New() (*Injector, error) {
	coreGraphics, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	coreFoundation, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	injector := &Injector{tracker: common.NewTracker(), pressedMouseButton: make(map[uint16]struct{})}
	purego.RegisterLibFunc(&injector.createMouse, coreGraphics, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&injector.createKeyboard, coreGraphics, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&injector.createScroll, coreGraphics, "CGEventCreateScrollWheelEvent2")
	purego.RegisterLibFunc(&injector.createEvent, coreGraphics, "CGEventCreate")
	purego.RegisterLibFunc(&injector.getLocation, coreGraphics, "CGEventGetLocation")
	purego.RegisterLibFunc(&injector.post, coreGraphics, "CGEventPost")
	purego.RegisterLibFunc(&injector.preflightPost, coreGraphics, "CGPreflightPostEventAccess")
	purego.RegisterLibFunc(&injector.release, coreFoundation, "CFRelease")
	return injector, nil
}

func (i *Injector) CanInject() bool {
	return i.preflightPost()
}

func (i *Injector) Inject(event common.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if event.Kind == common.ReleaseAll {
		return i.releaseAllLocked()
	}
	return i.injectLocked(event)
}

func (i *Injector) ReleaseAll() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.releaseAllLocked()
}

func (i *Injector) Close() error {
	return i.ReleaseAll()
}

func (i *Injector) injectLocked(event common.Event) error {
	var nativeEvent uintptr
	switch event.Kind {
	case common.MouseAbsolute, common.MouseRelative:
		location := point{X: float64(event.X), Y: float64(event.Y)}
		if event.Kind == common.MouseRelative {
			current := i.createEvent(0)
			if current == 0 {
				return errors.New("CGEventCreate 返回空事件")
			}
			location = i.getLocation(current)
			i.release(current)
			location.X += float64(event.X)
			location.Y += float64(event.Y)
		}
		eventType, button := i.moveType()
		nativeEvent = i.createMouse(0, eventType, location, button)
	case common.MouseButtonDown, common.MouseButtonUp:
		eventType, button, err := macMouseButton(event.Code, event.Kind == common.MouseButtonDown)
		if err != nil {
			return err
		}
		current := i.createEvent(0)
		if current == 0 {
			return errors.New("CGEventCreate 返回空事件")
		}
		location := i.getLocation(current)
		i.release(current)
		nativeEvent = i.createMouse(0, eventType, location, button)
	case common.MouseScroll:
		nativeEvent = i.createScroll(0, 0, 2, event.Y, event.X, 0)
	case common.KeyDown, common.KeyUp:
		keyCode, ok := macKeyCodes[event.Code]
		if !ok {
			return fmt.Errorf("macOS 暂不支持 HID 键码 %d", event.Code)
		}
		nativeEvent = i.createKeyboard(0, keyCode, event.Kind == common.KeyDown)
	default:
		return errors.New("macOS 不支持该输入事件")
	}
	if nativeEvent == 0 {
		return errors.New("CoreGraphics 无法创建输入事件")
	}
	i.post(0, nativeEvent)
	i.release(nativeEvent)
	i.tracker.Apply(event)
	if event.Kind == common.MouseButtonDown {
		i.pressedMouseButton[event.Code] = struct{}{}
	} else if event.Kind == common.MouseButtonUp {
		delete(i.pressedMouseButton, event.Code)
	}
	return nil
}

func (i *Injector) releaseAllLocked() error {
	events, _ := i.tracker.ReleaseEvents(1)
	var result error
	for _, event := range events {
		if err := i.injectLocked(event); err != nil {
			result = errors.Join(result, err)
		}
	}
	clear(i.pressedMouseButton)
	return result
}

func (i *Injector) moveType() (uint32, uint32) {
	if _, down := i.pressedMouseButton[1]; down {
		return 6, 0
	}
	if _, down := i.pressedMouseButton[2]; down {
		return 7, 1
	}
	for code := range i.pressedMouseButton {
		return 27, uint32(code - 1)
	}
	return 5, 0
}

func macMouseButton(code uint16, down bool) (uint32, uint32, error) {
	switch code {
	case 1:
		if down {
			return 1, 0, nil
		}
		return 2, 0, nil
	case 2:
		if down {
			return 3, 1, nil
		}
		return 4, 1, nil
	case 3, 4, 5:
		if down {
			return 25, uint32(code - 1), nil
		}
		return 26, uint32(code - 1), nil
	default:
		return 0, 0, errors.New("macOS 鼠标按钮无效")
	}
}

var macKeyCodes = map[uint16]uint16{
	4: 0, 5: 11, 6: 8, 7: 2, 8: 14, 9: 3, 10: 5, 11: 4, 12: 34, 13: 38, 14: 40, 15: 37, 16: 46, 17: 45, 18: 31, 19: 35, 20: 12, 21: 15, 22: 1, 23: 17, 24: 32, 25: 9, 26: 13, 27: 7, 28: 16, 29: 6,
	30: 18, 31: 19, 32: 20, 33: 21, 34: 23, 35: 22, 36: 26, 37: 28, 38: 25, 39: 29, 40: 36, 41: 53, 42: 51, 43: 48, 44: 49, 45: 27, 46: 24, 47: 33, 48: 30, 49: 42, 51: 41, 52: 39, 53: 50, 54: 43, 55: 47, 56: 44, 57: 57,
	58: 122, 59: 120, 60: 99, 61: 118, 62: 96, 63: 97, 64: 98, 65: 100, 66: 101, 67: 109, 68: 103, 69: 111, 73: 114, 74: 115, 75: 116, 76: 117, 77: 119, 78: 121, 79: 124, 80: 123, 81: 125, 82: 126,
	224: 59, 225: 56, 226: 58, 227: 55, 228: 62, 229: 60, 230: 61, 231: 54,
}

var _ common.Injector = (*Injector)(nil)
