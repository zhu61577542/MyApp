//go:build windows

package windowsclipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"
)

func clipboardForTest(t *testing.T) *Backend {
	t.Helper()
	if os.Getenv("MYAPP_WINDOWS_CLIPBOARD_TEST") != "1" {
		t.Skip("设置 MYAPP_WINDOWS_CLIPBOARD_TEST=1 后在交互桌面执行")
	}
	backend, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

func TestClipboardKeepsOneSystemThread(t *testing.T) {
	backend := clipboardForTest(t)
	release, err := backend.openClipboard(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	thread := syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")
	want, _, _ := thread.Call()
	for iteration := 0; iteration < 200; iteration++ {
		runtime.Gosched()
		got, _, _ := thread.Call()
		if got != want {
			t.Fatalf("剪贴板事务切换了系统线程: %d -> %d", want, got)
		}
	}
}

func TestClipboardTextAndFileRoundtrip(t *testing.T) {
	backend := clipboardForTest(t)
	ctx := context.Background()
	want := "MyApp 中文剪贴板\nsecond line 🚀"
	if err := backend.WriteText(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, _, err := backend.ReadText(ctx)
	if err != nil || got != want {
		t.Fatalf("文字往返错误: %v", err)
	}
	file := filepath.Join(t.TempDir(), "中文 文件.txt")
	if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	files := &FileBackend{base: backend}
	if err := files.WriteFiles(ctx, []string{file}); err != nil {
		t.Fatal(err)
	}
	paths, _, available, err := files.ReadFiles(ctx)
	if err != nil || !available || !reflect.DeepEqual(paths, []string{file}) {
		t.Fatalf("文件往返错误: %v", err)
	}
}

func TestClipboardConcurrentBackends(t *testing.T) {
	text := clipboardForTest(t)
	files, err := NewFiles()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	failures := make(chan error, 8)
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for iteration := 0; iteration < 20; iteration++ {
				if err := text.WriteText(ctx, "MyApp concurrent clipboard"); err != nil {
					failures <- err
					return
				}
				if _, _, err := text.ReadText(ctx); err != nil {
					failures <- err
					return
				}
				if err := files.WriteFiles(ctx, []string{path}); err != nil {
					failures <- err
					return
				}
				if _, _, _, err := files.ReadFiles(ctx); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestClipboardRetriesBusyAndHonorsCancellation(t *testing.T) {
	backend := clipboardForTest(t)
	for _, cancelEarly := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelEarly), func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			opened, _, err := backend.open.Call(0)
			if opened == 0 {
				t.Fatal(clipboardError(err, "无法建立占用剪贴板的测试条件"))
			}
			defer func() {
				if opened != 0 {
					backend.close.Call()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- backend.WriteText(ctx, "MyApp clipboard retry") }()
			time.Sleep(40 * time.Millisecond)
			if cancelEarly {
				cancel()
			} else {
				backend.close.Call()
				opened = 0
			}
			select {
			case err := <-result:
				if cancelEarly && !errors.Is(err, context.Canceled) || !cancelEarly && err != nil {
					t.Fatalf("占用/取消处理错误: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("剪贴板调用未结束")
			}
		})
	}
}
