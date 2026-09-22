package transfer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateRelativePathRejectsTraversal(t *testing.T) {
	invalid := []string{"", ".", "..", "../x", "/x", "a/../x", "a\\b", "a//b"}
	for _, value := range invalid {
		if ValidateRelativePath(value) == nil {
			t.Errorf("应拒绝 %q", value)
		}
	}
}

func TestManifestRejectsTransferIDTraversal(t *testing.T) {
	now := time.Now()
	manifest := Manifest{TransferID: "../escape", Entries: []Entry{{Path: "x", Type: File, ModifiedAt: now, SHA256: digest(nil)}}, CreatedAt: now}
	if manifest.Validate() == nil {
		t.Fatal("应拒绝越界传输 ID")
	}
}

func TestReceiverResumeVerifyAndCommit(t *testing.T) {
	content := []byte("跨平台文件内容")
	now := time.Now().UTC().Truncate(time.Second)
	entry := Entry{Path: "目录/文件.txt", Type: File, Size: int64(len(content)), Mode: 0o640, ModifiedAt: now, SHA256: digest(content)}
	manifest := Manifest{TransferID: "transfer-1", Entries: []Entry{{Path: "目录", Type: Directory, Mode: 0o750, ModifiedAt: now}, entry}, CreatedAt: now}
	cache := t.TempDir()
	receiver, err := NewReceiver(cache, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if written, err := receiver.Write(entry.Path, 0, bytes.NewReader(content[:5])); err != nil || written != 5 {
		t.Fatalf("第一次 Write() = %d, %v", written, err)
	}
	receiver, err = LoadReceiver(cache, manifest.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if offset, err := receiver.ResumeOffset(entry.Path); err != nil || offset != 5 {
		t.Fatalf("ResumeOffset() = %d, %v", offset, err)
	}
	if _, err := receiver.Write(entry.Path, 5, bytes.NewReader(content[5:])); err != nil {
		t.Fatal(err)
	}
	if err := receiver.CompleteFile(entry.Path); err != nil {
		t.Fatal(err)
	}
	final, err := receiver.Commit()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(final, filepath.FromSlash(entry.Path)))
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("提交内容错误: %q, %v", got, err)
	}
}

func TestManifestSupportsSizesAboveFourGiB(t *testing.T) {
	now := time.Now()
	entry := Entry{Path: "huge.bin", Type: File, Size: int64(5) << 30, Mode: 0o600, ModifiedAt: now, SHA256: digest(nil)}
	manifest := Manifest{TransferID: "large", Entries: []Entry{entry}, CreatedAt: now}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
}
