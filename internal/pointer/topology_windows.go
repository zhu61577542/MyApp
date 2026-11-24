//go:build windows

package pointer

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

type monitorCollection struct {
	info    *syscall.LazyProc
	screens []Screen
	err     error
}

var monitorCollections sync.Map
var monitorToken atomic.Uint64
var enumerateMonitors = syscall.NewLazyDLL("user32.dll").NewProc("EnumDisplayMonitors")
var monitorCallback = syscall.NewCallback(func(monitor, dc, rect, data uintptr) uintptr {
	value, found := monitorCollections.Load(data)
	if !found {
		return 0
	}
	c := value.(*monitorCollection)
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	ok, _, err := c.info.Call(monitor, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		c.err = fmt.Errorf("读取显示器信息失败: %v", err)
		return 0
	}
	if len(c.screens) >= 32 {
		c.err = fmt.Errorf("显示器超过支持数量")
		return 0
	}
	c.screens = append(c.screens, Screen{ID: fmt.Sprintf("%x", monitor), X: int(info.Monitor.Left), Y: int(info.Monitor.Top), Width: int(info.Monitor.Right - info.Monitor.Left), Height: int(info.Monitor.Bottom - info.Monitor.Top)})
	return 1
})

func (p *nativeBackend) Topology() (Topology, error) {
	if err := enumerateMonitors.Find(); err != nil {
		return Topology{}, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previous, _, _ := p.dpi.Call(^uintptr(3))
	if previous != 0 {
		defer p.dpi.Call(previous)
	}
	c := &monitorCollection{info: p.info}
	token := uintptr(monitorToken.Add(1))
	monitorCollections.Store(token, c)
	defer monitorCollections.Delete(token)
	ok, _, err := enumerateMonitors.Call(0, 0, monitorCallback, token)
	runtime.KeepAlive(c)
	if c.err != nil {
		return Topology{}, c.err
	}
	if ok == 0 {
		return Topology{}, fmt.Errorf("枚举显示器失败: %v", err)
	}
	return NewTopology(c.screens)
}
