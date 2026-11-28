//go:build windows

package pointer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	common "myapp/internal/input"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type nativePoint struct{ X, Y int32 }
type nativeRect struct{ Left, Top, Right, Bottom int32 }
type monitorInfo struct {
	Size          uint32
	Monitor, Work nativeRect
	Flags         uint32
}
type nativeBackend struct {
	monitor, info, dpi *syscall.LazyProc
	cursorRead         func(*nativePoint) error
	cursorWarp         func(Point) error
	cursorContext      func(uintptr) (uintptr, error)
}

func New() (Backend, error) {
	user32 := syscall.NewLazyDLL("user32.dll")
	position, warp := user32.NewProc("GetPhysicalCursorPos"), user32.NewProc("SetPhysicalCursorPos")
	p := &nativeBackend{monitor: user32.NewProc("MonitorFromPoint"), info: user32.NewProc("GetMonitorInfoW"), dpi: user32.NewProc("SetThreadDpiAwarenessContext")}
	for _, proc := range []*syscall.LazyProc{position, warp, p.monitor, p.info, p.dpi} {
		if err := proc.Find(); err != nil {
			return nil, err
		}
	}
	p.cursorRead = func(point *nativePoint) error {
		ok, _, err := position.Call(uintptr(unsafe.Pointer(point)))
		if ok == 0 {
			return fmt.Errorf("读取 Windows 光标失败: %v", err)
		}
		return nil
	}
	p.cursorWarp = func(point Point) error {
		ok, _, err := warp.Call(uintptr(int32(point.X)), uintptr(int32(point.Y)))
		if ok == 0 {
			return fmt.Errorf("恢复 Windows 光标位置失败: %v", err)
		}
		return nil
	}
	p.cursorContext = func(context uintptr) (uintptr, error) {
		previous, _, err := p.dpi.Call(context)
		if previous == 0 {
			return 0, fmt.Errorf("设置 Windows 光标 DPI 上下文失败: %v", err)
		}
		return previous, nil
	}
	return p, nil
}
func (p *nativeBackend) Screen() (Screen, error) {
	var screen Screen
	err := p.withPhysicalCoordinates(func() error {
		var err error
		screen, err = p.physicalScreen()
		return err
	})
	return screen, err
}

func (p *nativeBackend) physicalScreen() (Screen, error) {
	monitor, _, _ := p.monitor.Call(0, 1)
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	ok, _, err := p.info.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return Screen{}, fmt.Errorf("读取 Windows 主屏失败: %v", err)
	}
	screen := Screen{ID: fmt.Sprintf("%x", monitor), X: int(info.Monitor.Left), Y: int(info.Monitor.Top), Width: int(info.Monitor.Right - info.Monitor.Left), Height: int(info.Monitor.Bottom - info.Monitor.Top)}
	if !screen.Valid() {
		return Screen{}, errors.New("Windows 主屏几何无效")
	}
	return screen, nil
}
func (p *nativeBackend) Position() (Point, error) {
	var point nativePoint
	err := p.withPhysicalCoordinates(func() error { return p.cursorRead(&point) })
	return Point{int(point.X), int(point.Y)}, err
}
func (p *nativeBackend) Warp(point Point) error {
	return p.withPhysicalCoordinates(func() error {
		cancel := common.ExpectCursorWarp(int32(point.X), int32(point.Y))
		err := p.cursorWarp(point)
		if err != nil {
			cancel()
		}
		return err
	})
}

func (p *nativeBackend) withPhysicalCoordinates(action func() error) (result error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previous, err := p.cursorContext(^uintptr(3))
	if err != nil {
		return err
	}
	defer func() { _, err := p.cursorContext(previous); result = errors.Join(result, err) }()
	return action()
}

func (p *nativeBackend) Hide(ctx context.Context) (func() error, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, "cursor-guard")
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		input.Close()
		return nil, err
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		input.Close()
		return nil, err
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	ready := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(output).ReadString('\n')
		if err == nil && line != "hidden\n" && line != "hidden\r\n" {
			err = errors.New("光标看护返回无效状态")
		}
		ready <- err
	}()
	var once sync.Once
	var result error
	restore := func() error {
		once.Do(func() {
			input.Close()
			select {
			case result = <-done:
			case <-time.After(3 * time.Second):
				_ = command.Process.Kill()
				<-done
				result = errors.New("光标看护尚未确认恢复")
			}
		})
		return result
	}
	select {
	case err := <-ready:
		if err != nil {
			restore()
			return nil, err
		}
		return restore, nil
	case <-ctx.Done():
		restore()
		return nil, ctx.Err()
	case <-time.After(3 * time.Second):
		restore()
		return nil, errors.New("光标隐藏启动超时")
	}
}

type cursorInfo struct {
	Size, Flags uint32
	Handle      uintptr
	Point       nativePoint
}
type iconInfo struct {
	Icon        int32
	X, Y        uint32
	Mask, Color uintptr
}
type bitmapInfo struct {
	Type, Width, Height, WidthBytes int32
	Planes, Pixel                   uint16
	Bits                            uintptr
}

func (p *nativeBackend) InspectCursor() (CursorState, error) {
	u := syscall.NewLazyDLL("user32.dll")
	g := syscall.NewLazyDLL("gdi32.dll")
	info := cursorInfo{Size: uint32(unsafe.Sizeof(cursorInfo{}))}
	ok, _, err := u.NewProc("GetCursorInfo").Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return CursorState{}, err
	}
	state := CursorState{Visible: info.Flags&1 != 0}
	for _, id := range []uintptr{32512, 32513, 32514, 32515, 32516, 32642, 32643, 32644, 32645, 32646, 32648, 32649, 32650, 32651} {
		cursor, _, _ := u.NewProc("LoadCursorW").Call(0, id)
		if cursor == 0 {
			continue
		}
		var icon iconInfo
		ok, _, _ := u.NewProc("GetIconInfo").Call(cursor, uintptr(unsafe.Pointer(&icon)))
		if ok == 0 {
			continue
		}
		state.CheckedStandard++
		var bitmap bitmapInfo
		g.NewProc("GetObjectW").Call(icon.Mask, unsafe.Sizeof(bitmap), uintptr(unsafe.Pointer(&bitmap)))
		size := int64(bitmap.WidthBytes) * int64(bitmap.Height)
		if icon.Color == 0 && size > 0 && size <= 65536 && size%2 == 0 {
			bits := make([]byte, int(size))
			count, _, _ := g.NewProc("GetBitmapBits").Call(icon.Mask, uintptr(size), uintptr(unsafe.Pointer(&bits[0])))
			transparent := int64(count) == size
			for index, value := range bits {
				expected := byte(0)
				if index < len(bits)/2 {
					expected = 0xff
				}
				if value != expected {
					transparent = false
					break
				}
			}
			if transparent {
				state.TransparentStandard++
			}
		}
		if icon.Mask != 0 {
			g.NewProc("DeleteObject").Call(icon.Mask)
		}
		if icon.Color != 0 {
			g.NewProc("DeleteObject").Call(icon.Color)
		}
	}
	return state, nil
}

func RunGuard(input io.Reader, output io.Writer) error { return runWindowGuard(input, output) }
