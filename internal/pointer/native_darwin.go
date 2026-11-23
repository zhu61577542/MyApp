//go:build darwin

package pointer

import (
	"context"
	"errors"
	"fmt"
	"github.com/ebitengine/purego"
	"sync"
	"unsafe"
)

type cgPoint struct{ X, Y float64 }
type cgSize struct{ Width, Height float64 }
type cgRect struct {
	Origin cgPoint
	Size   cgSize
}
type nativeBackend struct {
	mainDisplay func() uint32
	bounds      func(uint32) cgRect
	createEvent func(uintptr) uintptr
	location    func(uintptr) cgPoint
	release     func(uintptr)
	warp        func(cgPoint) int32
	hide        func(uint32) int32
	show        func(uint32) int32
	visible     func() uint32
	background  func(bool) error
}

func New() (Backend, error) {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, err
	}
	p := &nativeBackend{}
	purego.RegisterLibFunc(&p.mainDisplay, cg, "CGMainDisplayID")
	purego.RegisterLibFunc(&p.bounds, cg, "CGDisplayBounds")
	purego.RegisterLibFunc(&p.createEvent, cg, "CGEventCreate")
	purego.RegisterLibFunc(&p.location, cg, "CGEventGetLocation")
	purego.RegisterLibFunc(&p.warp, cg, "CGWarpMouseCursorPosition")
	purego.RegisterLibFunc(&p.hide, cg, "CGDisplayHideCursor")
	purego.RegisterLibFunc(&p.show, cg, "CGDisplayShowCursor")
	purego.RegisterLibFunc(&p.visible, cg, "CGCursorIsVisible")
	purego.RegisterLibFunc(&p.release, cf, "CFRelease")
	p.background = backgroundCursor(cg, cf, p.release)
	return p, nil
}

// 后台光标属性是非公开接口，动态检测，缺失时拒绝隐藏。
func backgroundCursor(cg, cf uintptr, release func(uintptr)) func(bool) error {
	connection, err := purego.Dlsym(cg, "_CGSDefaultConnection")
	property, other := purego.Dlsym(cg, "CGSSetConnectionProperty")
	if err != nil || other != nil {
		return func(bool) error { return errors.New("当前 macOS 未提供后台光标管理接口") }
	}
	var current func() int32
	var set func(int32, int32, uintptr, uintptr) int32
	var create func(uintptr, string, uint32) uintptr
	purego.RegisterFunc(&current, connection)
	purego.RegisterFunc(&set, property)
	purego.RegisterLibFunc(&create, cf, "CFStringCreateWithCString")
	trueAddress, trueErr := purego.Dlsym(cf, "kCFBooleanTrue")
	falseAddress, falseErr := purego.Dlsym(cf, "kCFBooleanFalse")
	if trueErr != nil || falseErr != nil {
		return func(bool) error { return errors.New("无法加载后台光标状态") }
	}
	return func(enabled bool) error {
		key := create(0, "SetsCursorInBackground", 0x08000100)
		if key == 0 {
			return errors.New("无法创建后台光标属性")
		}
		defer release(key)
		address := falseAddress
		if enabled {
			address = trueAddress
		}
		id := current()
		if code := set(id, id, key, *(*uintptr)(unsafe.Pointer(address))); code != 0 {
			return fmt.Errorf("设置后台光标属性失败: %d", code)
		}
		return nil
	}
}

func (p *nativeBackend) InspectCursor() (CursorState, error) {
	return CursorState{Visible: p.visible() != 0}, nil
}
func (p *nativeBackend) Screen() (Screen, error) {
	id := p.mainDisplay()
	bounds := p.bounds(id)
	screen := Screen{ID: fmt.Sprintf("%x", id), X: int(bounds.Origin.X), Y: int(bounds.Origin.Y), Width: int(bounds.Size.Width), Height: int(bounds.Size.Height)}
	if !screen.Valid() {
		return Screen{}, errors.New("无法读取 Mac 主屏幕几何")
	}
	return screen, nil
}
func (p *nativeBackend) Position() (Point, error) {
	event := p.createEvent(0)
	if event == 0 {
		return Point{}, errors.New("无法读取 Mac 指针")
	}
	defer p.release(event)
	point := p.location(event)
	return Point{int(point.X), int(point.Y)}, nil
}
func (p *nativeBackend) Warp(point Point) error {
	if code := p.warp(cgPoint{float64(point.X), float64(point.Y)}); code != 0 {
		return fmt.Errorf("恢复 Mac 指针失败: %d", code)
	}
	return nil
}
func (p *nativeBackend) Hide(ctx context.Context) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.background(true); err != nil {
		return nil, err
	}
	if code := p.hide(p.mainDisplay()); code != 0 {
		_ = p.background(false)
		return nil, fmt.Errorf("隐藏 Mac 光标失败: %d", code)
	}
	var once sync.Once
	var result error
	return func() error {
		once.Do(func() {
			if code := p.show(p.mainDisplay()); code != 0 {
				result = fmt.Errorf("恢复 Mac 光标失败: %d", code)
			}
			result = errors.Join(result, p.background(false))
		})
		return result
	}, nil
}
