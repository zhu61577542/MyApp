//go:build x11integration

package linuxclipboard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestX11ClipboardRoundTrip(t *testing.T) {
	backend, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	want := "MyApp 中文 clipboard\nsecond line"
	if err := backend.WriteText(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, revision, err := backend.ReadText(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || revision == 0 {
		t.Fatalf("ReadText() = %q, %d", got, revision)
	}
}

func TestX11FileClipboardRoundTrip(t *testing.T) {
	backend, err := NewFiles()
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "中文 文件.txt")
	if err := os.WriteFile(filename, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := backend.WriteFiles(ctx, []string{filename}); err != nil {
		t.Fatal(err)
	}
	paths, revision, available, err := backend.ReadFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !available || revision == 0 || len(paths) != 1 || paths[0] != filename {
		t.Fatalf("ReadFiles() = %v, %d, %v", paths, revision, available)
	}
}
