package transfer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReceiverSymlinkCannotResumeCompleteOrCommit(t *testing.T) {
	content := []byte("data")
	r, err := NewReceiver(t.TempDir(), requirementManifest("a", content))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "a")
	if err := os.WriteFile(outside, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.partial, "a")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResumeOffset("a"); err == nil {
		t.Error("允许从外部文件恢复")
	}
	if err := r.CompleteFile("a"); err == nil {
		t.Error("允许完成外部文件")
	}
	if _, err := r.Commit(); err == nil {
		t.Error("允许提交外部文件")
	}
	if err := r.Cancel(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("取消传输影响了外部文件")
	}
	if _, err := LoadReceiver(r.root, "audit"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("取消后恢复状态仍存在: %v", err)
	}
}

func TestReceiverCommitConflictPreservesRecovery(t *testing.T) {
	content := []byte("data")
	r, err := NewReceiver(t.TempDir(), requirementManifest("a", content))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("a", 0, bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), r.final); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(); err == nil {
		t.Fatal("覆盖了已有缓存路径")
	}
	restored, err := LoadReceiver(r.root, "audit")
	if err != nil {
		t.Fatal(err)
	}
	if offset, err := restored.ResumeOffset("a"); err != nil || offset != int64(len(content)) {
		t.Fatalf("冲突后丢失进度: %d %v", offset, err)
	}
}

func requirementManifest(name string, content []byte) Manifest {
	now := time.Now()
	return Manifest{TransferID: "audit", CreatedAt: now, Entries: []Entry{{Path: name, Type: File, Size: int64(len(content)), Mode: 0600, ModifiedAt: now, SHA256: digest(content)}}}
}

func TestRequirementCorruptFileCannotCommit(t *testing.T) {
	r, err := NewReceiver(t.TempDir(), requirementManifest("a", []byte("good")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("a", 0, bytes.NewBufferString("evil")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(); err == nil {
		t.Fatal("损坏文件被提交")
	}
}

func TestRequirementTruncatedFileCannotCommit(t *testing.T) {
	r, err := NewReceiver(t.TempDir(), requirementManifest("a", []byte("good")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("a", 0, bytes.NewBufferString("go")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(); err == nil {
		t.Fatal("未完整接收的文件被提交")
	}
}

func TestRequirementUnlistedPathCannotBeWritten(t *testing.T) {
	r, err := NewReceiver(t.TempDir(), requirementManifest("a", []byte("good")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write("../outside", 0, bytes.NewBufferString("bad")); err == nil {
		t.Fatal("允许写入清单外路径")
	}
}

func TestRequirementReceiverRejectsSymlinkEscape(t *testing.T) {
	cache := t.TempDir()
	outside := t.TempDir()
	r, err := NewReceiver(cache, requirementManifest("dir/a", []byte("data")))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.partial, "dir")); err != nil {
		t.Fatal(err)
	}
	_, writeErr := r.Write("dir/a", 0, bytes.NewBufferString("data"))
	if writeErr == nil {
		t.Error("接收端跟随符号链接写入缓存外目录")
	}
	if _, err := os.Stat(filepath.Join(outside, "a")); err == nil {
		t.Error("缓存外文件已被创建")
	}
}

func TestRequirementUserFileCannotCollideWithResumeMetadata(t *testing.T) {
	content := []byte("user content")
	r, err := NewReceiver(t.TempDir(), requirementManifest(".myapp-transfer.json", content))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write(".myapp-transfer.json", 0, bytes.NewReader(content)); err != nil {
		t.Fatalf("合法用户文件与内部续传元数据冲突: %v", err)
	}
	final, err := r.Commit()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(final, ".myapp-transfer.json"))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("用户文件丢失: %v", err)
	}
}
