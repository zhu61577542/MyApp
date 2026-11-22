//go:build darwin

package darwinclipboard

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileClipboardRoundTrip(t *testing.T) {
	if os.Getenv("MYAPP_MAC_CLIPBOARD_TEST") != "1" {
		t.Skip("需要显式启用真实 Mac 剪贴板测试")
	}
	ctx := context.Background()
	backend := NewFiles()
	directory := t.TempDir()
	file := filepath.Join(directory, "中文 space.txt")
	if err := os.WriteFile(file, []byte("clipboard"), 0600); err != nil {
		t.Fatal(err)
	}
	want := []string{file, directory}
	if err := backend.WriteFiles(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, _, available, err := backend.ReadFiles(ctx)
	if err != nil || !available || !reflect.DeepEqual(got, want) {
		t.Fatalf("读取文件剪贴板: paths=%q available=%v err=%v", got, available, err)
	}
	if err := New().WriteText(ctx, "Mac text 中文"); err != nil {
		t.Fatal(err)
	}
	got, _, available, err = backend.ReadFiles(ctx)
	if err != nil || available || len(got) != 0 {
		t.Fatalf("纯文字被识别为文件: paths=%q available=%v err=%v", got, available, err)
	}
}
