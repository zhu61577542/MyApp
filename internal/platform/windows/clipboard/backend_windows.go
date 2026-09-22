//go:build windows

package windowsclipboard

import (
	"context"
	"errors"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	unicodeText  = 13
	moveableZero = 0x0042
)

type Backend struct {
	open         *syscall.LazyProc
	close        *syscall.LazyProc
	empty        *syscall.LazyProc
	available    *syscall.LazyProc
	getData      *syscall.LazyProc
	setData      *syscall.LazyProc
	sequence     *syscall.LazyProc
	globalAlloc  *syscall.LazyProc
	globalFree   *syscall.LazyProc
	globalLock   *syscall.LazyProc
	globalUnlock *syscall.LazyProc
	globalSize   *syscall.LazyProc
}

func New() (*Backend, error) {
	user32 := syscall.NewLazyDLL("user32.dll")
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	backend := &Backend{
		open: user32.NewProc("OpenClipboard"), close: user32.NewProc("CloseClipboard"),
		empty: user32.NewProc("EmptyClipboard"), available: user32.NewProc("IsClipboardFormatAvailable"),
		getData: user32.NewProc("GetClipboardData"), setData: user32.NewProc("SetClipboardData"),
		sequence: user32.NewProc("GetClipboardSequenceNumber"), globalAlloc: kernel32.NewProc("GlobalAlloc"),
		globalFree: kernel32.NewProc("GlobalFree"), globalLock: kernel32.NewProc("GlobalLock"),
		globalUnlock: kernel32.NewProc("GlobalUnlock"), globalSize: kernel32.NewProc("GlobalSize"),
	}
	for _, procedure := range []*syscall.LazyProc{backend.open, backend.close, backend.empty, backend.available, backend.getData, backend.setData, backend.sequence, backend.globalAlloc, backend.globalFree, backend.globalLock, backend.globalUnlock, backend.globalSize} {
		if err := procedure.Find(); err != nil {
			return nil, err
		}
	}
	return backend, nil
}

func (b *Backend) ReadText(ctx context.Context) (string, uint64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	revision, _, _ := b.sequence.Call()
	available, _, _ := b.available.Call(unicodeText)
	if available == 0 {
		return "", uint64(revision), nil
	}
	if opened, _, callErr := b.open.Call(0); opened == 0 {
		return "", 0, clipboardError(callErr, "无法打开 Windows 剪贴板")
	}
	defer b.close.Call()
	handle, _, callErr := b.getData.Call(unicodeText)
	if handle == 0 {
		return "", 0, clipboardError(callErr, "无法读取 Windows 剪贴板")
	}
	pointer, _, callErr := b.globalLock.Call(handle)
	if pointer == 0 {
		return "", 0, clipboardError(callErr, "无法锁定 Windows 剪贴板内存")
	}
	defer b.globalUnlock.Call(handle)
	size, _, _ := b.globalSize.Call(handle)
	if size < 2 {
		return "", uint64(revision), nil
	}
	units := unsafe.Slice((*uint16)(unsafe.Pointer(pointer)), int(size/2))
	end := 0
	for end < len(units) && units[end] != 0 {
		end++
	}
	return string(utf16.Decode(units[:end])), uint64(revision), nil
}

func (b *Backend) WriteText(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	units := utf16.Encode([]rune(text))
	units = append(units, 0)
	handle, _, callErr := b.globalAlloc.Call(moveableZero, uintptr(len(units)*2))
	if handle == 0 {
		return clipboardError(callErr, "无法分配 Windows 剪贴板内存")
	}
	owned := true
	defer func() {
		if owned {
			b.globalFree.Call(handle)
		}
	}()
	pointer, _, callErr := b.globalLock.Call(handle)
	if pointer == 0 {
		return clipboardError(callErr, "无法锁定 Windows 剪贴板内存")
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(pointer)), len(units)), units)
	b.globalUnlock.Call(handle)
	if opened, _, callErr := b.open.Call(0); opened == 0 {
		return clipboardError(callErr, "无法打开 Windows 剪贴板")
	}
	defer b.close.Call()
	if emptied, _, callErr := b.empty.Call(); emptied == 0 {
		return clipboardError(callErr, "无法清空 Windows 剪贴板")
	}
	if result, _, callErr := b.setData.Call(unicodeText, handle); result == 0 {
		return clipboardError(callErr, "无法写入 Windows 剪贴板")
	}
	owned = false
	return nil
}

func clipboardError(err error, fallback string) error {
	if err != nil && err != syscall.Errno(0) {
		return err
	}
	return errors.New(fallback)
}
