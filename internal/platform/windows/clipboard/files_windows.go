//go:build windows

package windowsclipboard

import (
	"context"
	"errors"
	"unicode/utf16"
	"unsafe"
)

const fileDrop = 15

type FileBackend struct {
	base *Backend
}

func NewFiles() (*FileBackend, error) {
	backend, err := New()
	if err != nil {
		return nil, err
	}
	return &FileBackend{base: backend}, nil
}

func (b *FileBackend) ReadFiles(ctx context.Context) ([]string, uint64, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}
	revision, _, _ := b.base.sequence.Call()
	available, _, _ := b.base.available.Call(fileDrop)
	if available == 0 {
		return nil, uint64(revision), false, nil
	}
	if opened, _, callErr := b.base.open.Call(0); opened == 0 {
		return nil, 0, false, clipboardError(callErr, "无法打开 Windows 剪贴板")
	}
	defer b.base.close.Call()
	handle, _, callErr := b.base.getData.Call(fileDrop)
	if handle == 0 {
		return nil, 0, false, clipboardError(callErr, "无法读取 Windows 文件剪贴板")
	}
	pointer, _, callErr := b.base.globalLock.Call(handle)
	if pointer == 0 {
		return nil, 0, false, clipboardError(callErr, "无法锁定 Windows 文件剪贴板")
	}
	defer b.base.globalUnlock.Call(handle)
	size, _, _ := b.base.globalSize.Call(handle)
	if size < 22 {
		return nil, 0, false, errors.New("Windows DROPFILES 数据过短")
	}
	header := unsafe.Slice((*byte)(unsafe.Pointer(pointer)), 20)
	offset := *(*uint32)(unsafe.Pointer(&header[0]))
	wide := *(*uint32)(unsafe.Pointer(&header[16]))
	if wide == 0 || offset < 20 || uintptr(offset) >= size || (size-uintptr(offset))%2 != 0 {
		return nil, 0, false, errors.New("Windows DROPFILES 头无效")
	}
	units := unsafe.Slice((*uint16)(unsafe.Pointer(pointer+uintptr(offset))), int((size-uintptr(offset))/2))
	var paths []string
	for start := 0; start < len(units) && units[start] != 0; {
		end := start
		for end < len(units) && units[end] != 0 {
			end++
		}
		if end == len(units) {
			return nil, 0, false, errors.New("Windows DROPFILES 字符串未终止")
		}
		paths = append(paths, string(utf16.Decode(units[start:end])))
		start = end + 1
	}
	return paths, uint64(revision), len(paths) > 0, nil
}

func (b *FileBackend) WriteFiles(ctx context.Context, paths []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("文件路径不能为空")
	}
	units := make([]uint16, 0)
	for _, path := range paths {
		if path == "" {
			return errors.New("文件路径不能为空")
		}
		units = append(units, utf16.Encode([]rune(path))...)
		units = append(units, 0)
	}
	units = append(units, 0)
	size := uintptr(20 + len(units)*2)
	handle, _, callErr := b.base.globalAlloc.Call(moveableZero, size)
	if handle == 0 {
		return clipboardError(callErr, "无法分配 Windows 文件剪贴板内存")
	}
	owned := true
	defer func() {
		if owned {
			b.base.globalFree.Call(handle)
		}
	}()
	pointer, _, callErr := b.base.globalLock.Call(handle)
	if pointer == 0 {
		return clipboardError(callErr, "无法锁定 Windows 文件剪贴板内存")
	}
	header := unsafe.Slice((*byte)(unsafe.Pointer(pointer)), 20)
	*(*uint32)(unsafe.Pointer(&header[0])) = 20
	*(*uint32)(unsafe.Pointer(&header[16])) = 1
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(pointer+20)), len(units)), units)
	b.base.globalUnlock.Call(handle)
	if opened, _, callErr := b.base.open.Call(0); opened == 0 {
		return clipboardError(callErr, "无法打开 Windows 剪贴板")
	}
	defer b.base.close.Call()
	if emptied, _, callErr := b.base.empty.Call(); emptied == 0 {
		return clipboardError(callErr, "无法清空 Windows 剪贴板")
	}
	if result, _, callErr := b.base.setData.Call(fileDrop, handle); result == 0 {
		return clipboardError(callErr, "无法写入 Windows 文件剪贴板")
	}
	owned = false
	return nil
}
