//go:build linux

package linuxinput

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	common "myapp/internal/input"
)

const (
	keyPress      = 2
	keyRelease    = 3
	buttonPress   = 4
	buttonRelease = 5
	motionNotify  = 6

	buttonPressMask   = 1 << 2
	buttonReleaseMask = 1 << 3
	pointerMotionMask = 1 << 6
)

type Capturer struct {
	mu              sync.Mutex
	display         uintptr
	root            uintptr
	screen          int32
	width           int32
	height          int32
	closeDisplay    func(uintptr) int32
	flush           func(uintptr) int32
	pending         func(uintptr) int32
	nextEvent       func(uintptr, *[192]byte) int32
	grabPointer     func(uintptr, uintptr, int32, uint32, int32, int32, uintptr, uintptr, uintptr) int32
	grabKeyboard    func(uintptr, uintptr, int32, int32, int32, uintptr) int32
	ungrabPointer   func(uintptr, uintptr) int32
	ungrabKeyboard  func(uintptr, uintptr) int32
	warpPointer     func(uintptr, uintptr, uintptr, int32, int32, uint32, uint32, int32, int32) int32
	keycodeToKeysym func(uintptr, uint8, int32, int32) uintptr
	keyByKeysym     map[uintptr]uint16
}

func NewCapturer(displayName string) (*Capturer, error) {
	x11, err := purego.Dlopen("libX11.so.6", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	var openDisplay func(*byte) uintptr
	var defaultRoot func(uintptr) uintptr
	var defaultScreen func(uintptr) int32
	var displayWidth func(uintptr, int32) int32
	var displayHeight func(uintptr, int32) int32
	var stringToKeysym func(*byte) uintptr
	capturer := &Capturer{keyByKeysym: make(map[uintptr]uint16)}
	purego.RegisterLibFunc(&openDisplay, x11, "XOpenDisplay")
	purego.RegisterLibFunc(&defaultRoot, x11, "XDefaultRootWindow")
	purego.RegisterLibFunc(&defaultScreen, x11, "XDefaultScreen")
	purego.RegisterLibFunc(&displayWidth, x11, "XDisplayWidth")
	purego.RegisterLibFunc(&displayHeight, x11, "XDisplayHeight")
	purego.RegisterLibFunc(&stringToKeysym, x11, "XStringToKeysym")
	purego.RegisterLibFunc(&capturer.closeDisplay, x11, "XCloseDisplay")
	purego.RegisterLibFunc(&capturer.flush, x11, "XFlush")
	purego.RegisterLibFunc(&capturer.pending, x11, "XPending")
	purego.RegisterLibFunc(&capturer.nextEvent, x11, "XNextEvent")
	purego.RegisterLibFunc(&capturer.grabPointer, x11, "XGrabPointer")
	purego.RegisterLibFunc(&capturer.grabKeyboard, x11, "XGrabKeyboard")
	purego.RegisterLibFunc(&capturer.ungrabPointer, x11, "XUngrabPointer")
	purego.RegisterLibFunc(&capturer.ungrabKeyboard, x11, "XUngrabKeyboard")
	purego.RegisterLibFunc(&capturer.warpPointer, x11, "XWarpPointer")
	purego.RegisterLibFunc(&capturer.keycodeToKeysym, x11, "XkbKeycodeToKeysym")
	var namePointer *byte
	if displayName != "" {
		name := append([]byte(displayName), 0)
		namePointer = &name[0]
	}
	capturer.display = openDisplay(namePointer)
	if capturer.display == 0 {
		return nil, errors.New("无法打开 X11 display")
	}
	capturer.screen = defaultScreen(capturer.display)
	capturer.root = defaultRoot(capturer.display)
	capturer.width = displayWidth(capturer.display, capturer.screen)
	capturer.height = displayHeight(capturer.display, capturer.screen)
	for hid, name := range xKeyNames {
		value := append([]byte(name), 0)
		if keysym := stringToKeysym(&value[0]); keysym != 0 {
			capturer.keyByKeysym[keysym] = hid
		}
	}
	return capturer, nil
}

func (c *Capturer) Capture(ctx context.Context, handler func(common.Event) error) error {
	if handler == nil {
		return errors.New("输入处理函数不能为空")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.display == 0 {
		return errors.New("X11 capturer 已关闭")
	}
	mask := uint32(buttonPressMask | buttonReleaseMask | pointerMotionMask)
	if result := c.grabPointer(c.display, c.root, 0, mask, 1, 1, 0, 0, 0); result != 0 {
		return fmt.Errorf("XGrabPointer 失败: %d", result)
	}
	defer c.ungrabPointer(c.display, 0)
	if result := c.grabKeyboard(c.display, c.root, 0, 1, 1, 0); result != 0 {
		return fmt.Errorf("XGrabKeyboard 失败: %d", result)
	}
	defer c.ungrabKeyboard(c.display, 0)
	centerX, centerY := c.width/2, c.height/2
	c.warpPointer(c.display, 0, c.root, 0, 0, 0, 0, centerX, centerY)
	c.flush(c.display)
	sequence := uint64(1)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.pending(c.display) == 0 {
			time.Sleep(2 * time.Millisecond)
			continue
		}
		var nativeEvent [192]byte
		c.nextEvent(c.display, &nativeEvent)
		eventType := int32(binary.LittleEndian.Uint32(nativeEvent[0:4]))
		var event common.Event
		emit := true
		switch eventType {
		case motionNotify:
			x := int32(binary.LittleEndian.Uint32(nativeEvent[72:76]))
			y := int32(binary.LittleEndian.Uint32(nativeEvent[76:80]))
			if x == centerX && y == centerY {
				emit = false
				break
			}
			event = common.Event{Kind: common.MouseRelative, Sequence: sequence, X: x - centerX, Y: y - centerY}
			c.warpPointer(c.display, 0, c.root, 0, 0, 0, 0, centerX, centerY)
			c.flush(c.display)
		case buttonPress, buttonRelease:
			button := binary.LittleEndian.Uint32(nativeEvent[84:88])
			if scrollEvent, ok := capturedScroll(button, sequence, eventType == buttonPress); ok {
				event = scrollEvent
			} else if code, ok := capturedButton(button); ok {
				kind := common.MouseButtonDown
				if eventType == buttonRelease {
					kind = common.MouseButtonUp
				}
				event = common.Event{Kind: kind, Sequence: sequence, Code: code}
			} else {
				emit = false
			}
		case keyPress, keyRelease:
			keycode := uint8(binary.LittleEndian.Uint32(nativeEvent[84:88]))
			keysym := c.keycodeToKeysym(c.display, keycode, 0, 0)
			code, ok := c.keyByKeysym[keysym]
			if !ok {
				emit = false
				break
			}
			kind := common.KeyDown
			if eventType == keyRelease {
				kind = common.KeyUp
			}
			event = common.Event{Kind: kind, Sequence: sequence, Code: code}
		default:
			emit = false
		}
		if emit {
			if err := event.Validate(); err != nil {
				return err
			}
			if err := handler(event); err != nil {
				return err
			}
			sequence++
		}
	}
}

func (c *Capturer) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.display != 0 {
		c.closeDisplay(c.display)
		c.display = 0
	}
	return nil
}

func capturedButton(button uint32) (uint16, bool) {
	switch button {
	case 1:
		return 1, true
	case 3:
		return 2, true
	case 2:
		return 3, true
	case 8:
		return 4, true
	case 9:
		return 5, true
	default:
		return 0, false
	}
}

func capturedScroll(button uint32, sequence uint64, press bool) (common.Event, bool) {
	if !press {
		return common.Event{}, false
	}
	switch button {
	case 4:
		return common.Event{Kind: common.MouseScroll, Sequence: sequence, Y: 1}, true
	case 5:
		return common.Event{Kind: common.MouseScroll, Sequence: sequence, Y: -1}, true
	case 6:
		return common.Event{Kind: common.MouseScroll, Sequence: sequence, X: -1}, true
	case 7:
		return common.Event{Kind: common.MouseScroll, Sequence: sequence, X: 1}, true
	default:
		return common.Event{}, false
	}
}
