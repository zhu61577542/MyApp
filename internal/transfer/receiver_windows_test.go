//go:build windows

package transfer

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReceiverRenameFailureKeepsWritableRecovery(t *testing.T) {
	content := []byte("readonly transfer")
	manifest := requirementManifest("dir/a", content)
	manifest.Entries[0].Mode = 0440
	manifest.Entries = append([]Entry{{Path: "dir", Type: Directory, Mode: 0550, ModifiedAt: manifest.CreatedAt}}, manifest.Entries...)
	receiver, err := NewReceiver(t.TempDir(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Write("dir/a", 0, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	path, err := syscall.UTF16PtrFromString(receiver.partial)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(path, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if handle != syscall.InvalidHandle {
			syscall.CloseHandle(handle)
		}
	}()
	if _, err := receiver.Commit(); err == nil {
		t.Fatal("持有不共享删除的目录句柄时提交成功")
	}
	info, err := os.Stat(filepath.Join(receiver.partial, "dir", "a"))
	if err != nil || info.Mode().Perm()&0200 == 0 {
		t.Fatalf("失败返回前未恢复文件可写权限: %v %v", info, err)
	}
	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	handle = syscall.InvalidHandle
	restored, err := LoadReceiver(receiver.root, manifest.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if offset, err := restored.ResumeOffset("dir/a"); err != nil || offset != int64(len(content)) {
		t.Fatalf("失败后丢失接收进度: %d %v", offset, err)
	}
	if _, err := restored.Write("dir/a", int64(len(content)), bytes.NewReader(nil)); err != nil {
		t.Fatalf("失败后临时内容不可写: %v", err)
	}
	if _, err := restored.Commit(); err != nil {
		t.Fatalf("解除占用后提交失败: %v", err)
	}
}

func TestReceiverRejectsWindowsJunctionEscape(t *testing.T) {
	outside := t.TempDir()
	want := []byte("external test data")
	if err := os.WriteFile(filepath.Join(outside, "a"), want, 0600); err != nil {
		t.Fatal(err)
	}
	receiver, err := NewReceiver(t.TempDir(), requirementManifest("dir/a", []byte("replacement")))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "cmd.exe", "/c", "mklink", "/J", filepath.Join(receiver.partial, "dir"), outside)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("创建测试 junction 失败: %v %s", err, output)
	}
	if _, err := receiver.Write("dir/a", 0, bytes.NewBufferString("replacement")); err == nil {
		t.Error("允许通过 junction 写入缓存外目录")
	}
	if _, err := LoadReceiver(receiver.root, "audit"); err == nil {
		t.Error("允许恢复包含 junction 的临时树")
	}
	if err := receiver.Cancel(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(outside, "a"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("外部测试文件被更改: %v", err)
	}
}
