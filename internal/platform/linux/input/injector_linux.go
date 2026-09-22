//go:build linux

package linuxinput

import (
	"errors"
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
	common "myapp/internal/input"
)

type Injector struct {
	mu              sync.Mutex
	display         uintptr
	defaultScreen   int32
	closeDisplay    func(uintptr) int32
	flush           func(uintptr) int32
	stringToKeysym  func(*byte) uintptr
	keysymToKeycode func(uintptr, uintptr) uint8
	fakeMotion      func(uintptr, int32, int32, int32, uintptr) int32
	fakeRelative    func(uintptr, int32, int32, uintptr) int32
	fakeButton      func(uintptr, uint32, int32, uintptr) int32
	fakeKey         func(uintptr, uint32, int32, uintptr) int32
	tracker         *common.Tracker
}

func New(displayName string) (*Injector, error) {
	x11, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	xtest, err := purego.Dlopen("libXtst.so.6", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	var openDisplay func(*byte) uintptr
	var defaultScreen func(uintptr) int32
	injector := &Injector{tracker: common.NewTracker()}
	purego.RegisterLibFunc(&openDisplay, x11, "XOpenDisplay")
	purego.RegisterLibFunc(&defaultScreen, x11, "XDefaultScreen")
	purego.RegisterLibFunc(&injector.closeDisplay, x11, "XCloseDisplay")
	purego.RegisterLibFunc(&injector.flush, x11, "XFlush")
	purego.RegisterLibFunc(&injector.stringToKeysym, x11, "XStringToKeysym")
	purego.RegisterLibFunc(&injector.keysymToKeycode, x11, "XKeysymToKeycode")
	purego.RegisterLibFunc(&injector.fakeMotion, xtest, "XTestFakeMotionEvent")
	purego.RegisterLibFunc(&injector.fakeRelative, xtest, "XTestFakeRelativeMotionEvent")
	purego.RegisterLibFunc(&injector.fakeButton, xtest, "XTestFakeButtonEvent")
	purego.RegisterLibFunc(&injector.fakeKey, xtest, "XTestFakeKeyEvent")
	var namePointer *byte
	if displayName != "" {
		name := append([]byte(displayName), 0)
		namePointer = &name[0]
	}
	injector.display = openDisplay(namePointer)
	if injector.display == 0 {
		return nil, errors.New("无法打开 X11 display")
	}
	injector.defaultScreen = defaultScreen(injector.display)
	return injector, nil
}

func (i *Injector) Inject(event common.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.display == 0 {
		return errors.New("X11 injector 已关闭")
	}
	if event.Kind == common.ReleaseAll {
		return i.releaseAllLocked()
	}
	return i.injectLocked(event)
}

func (i *Injector) ReleaseAll() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.display == 0 {
		return nil
	}
	return i.releaseAllLocked()
}

func (i *Injector) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.display == 0 {
		return nil
	}
	releaseErr := i.releaseAllLocked()
	i.closeDisplay(i.display)
	i.display = 0
	return releaseErr
}

func (i *Injector) injectLocked(event common.Event) error {
	var ok bool
	switch event.Kind {
	case common.MouseAbsolute:
		ok = i.fakeMotion(i.display, i.defaultScreen, event.X, event.Y, 0) != 0
	case common.MouseRelative:
		ok = i.fakeRelative(i.display, event.X, event.Y, 0) != 0
	case common.MouseButtonDown, common.MouseButtonUp:
		button, err := xButton(event.Code)
		if err != nil {
			return err
		}
		ok = i.fakeButton(i.display, button, boolInt(event.Kind == common.MouseButtonDown), 0) != 0
	case common.MouseScroll:
		if err := i.scroll(event.X, 6, 7); err != nil {
			return err
		}
		if err := i.scroll(event.Y, 5, 4); err != nil {
			return err
		}
		ok = true
	case common.KeyDown, common.KeyUp:
		name, exists := xKeyNames[event.Code]
		if !exists {
			return fmt.Errorf("X11 暂不支持 HID 键码 %d", event.Code)
		}
		keyName := append([]byte(name), 0)
		keysym := i.stringToKeysym(&keyName[0])
		keycode := i.keysymToKeycode(i.display, keysym)
		if keysym == 0 || keycode == 0 {
			return fmt.Errorf("X11 当前键盘布局没有按键 %s", name)
		}
		ok = i.fakeKey(i.display, uint32(keycode), boolInt(event.Kind == common.KeyDown), 0) != 0
	default:
		return errors.New("X11 不支持该输入事件")
	}
	if !ok || i.flush(i.display) == 0 {
		return errors.New("XTest 输入注入失败")
	}
	i.tracker.Apply(event)
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
	return result
}

func (i *Injector) scroll(amount int32, negativeButton, positiveButton uint32) error {
	button := positiveButton
	if amount < 0 {
		button = negativeButton
		amount = -amount
	}
	for count := int32(0); count < amount; count++ {
		if i.fakeButton(i.display, button, 1, 0) == 0 || i.fakeButton(i.display, button, 0, 0) == 0 {
			return errors.New("XTest 滚轮注入失败")
		}
	}
	return nil
}

func xButton(code uint16) (uint32, error) {
	switch code {
	case 1:
		return 1, nil
	case 2:
		return 3, nil
	case 3:
		return 2, nil
	case 4:
		return 8, nil
	case 5:
		return 9, nil
	default:
		return 0, errors.New("X11 鼠标按钮无效")
	}
}

func boolInt(value bool) int32 {
	if value {
		return 1
	}
	return 0
}

var xKeyNames = map[uint16]string{
	4: "a", 5: "b", 6: "c", 7: "d", 8: "e", 9: "f", 10: "g", 11: "h", 12: "i", 13: "j", 14: "k", 15: "l", 16: "m", 17: "n", 18: "o", 19: "p", 20: "q", 21: "r", 22: "s", 23: "t", 24: "u", 25: "v", 26: "w", 27: "x", 28: "y", 29: "z",
	30: "1", 31: "2", 32: "3", 33: "4", 34: "5", 35: "6", 36: "7", 37: "8", 38: "9", 39: "0", 40: "Return", 41: "Escape", 42: "BackSpace", 43: "Tab", 44: "space", 45: "minus", 46: "equal", 47: "bracketleft", 48: "bracketright", 49: "backslash", 51: "semicolon", 52: "apostrophe", 53: "grave", 54: "comma", 55: "period", 56: "slash", 57: "Caps_Lock",
	58: "F1", 59: "F2", 60: "F3", 61: "F4", 62: "F5", 63: "F6", 64: "F7", 65: "F8", 66: "F9", 67: "F10", 68: "F11", 69: "F12", 73: "Insert", 74: "Home", 75: "Page_Up", 76: "Delete", 77: "End", 78: "Page_Down", 79: "Right", 80: "Left", 81: "Down", 82: "Up",
	224: "Control_L", 225: "Shift_L", 226: "Alt_L", 227: "Super_L", 228: "Control_R", 229: "Shift_R", 230: "Alt_R", 231: "Super_R",
}

var _ common.Injector = (*Injector)(nil)
