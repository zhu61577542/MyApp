package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const stateFilename = ".myapp-transfer.json"

type Receiver struct {
	mu       sync.Mutex
	root     string
	manifest Manifest
	partial  string
	final    string
	entries  map[string]Entry
}

type stateEnvelope struct {
	Manifest Manifest `json:"manifest"`
	SHA256   string   `json:"sha256"`
}

func NewReceiver(cacheRoot string, manifest Manifest) (*Receiver, error) {
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(cacheRoot)
	if err != nil {
		return nil, err
	}
	partial := filepath.Join(root, ".partial-"+manifest.TransferID)
	final := filepath.Join(root, manifest.TransferID)
	if !within(root, partial) || !within(root, final) {
		return nil, errors.New("传输缓存路径越界")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(partial, 0o700); err != nil {
		return nil, err
	}
	receiver := newReceiverState(root, partial, final, manifest)
	for _, entry := range manifest.Entries {
		if entry.Type == Directory {
			if err := os.MkdirAll(receiver.path(entry.Path), os.FileMode(entry.Mode)); err != nil {
				return nil, err
			}
		}
	}
	if err := receiver.saveState(); err != nil {
		return nil, err
	}
	return receiver, nil
}

func LoadReceiver(cacheRoot, transferID string) (*Receiver, error) {
	if !validTransferID(transferID) {
		return nil, errors.New("传输 ID 无效")
	}
	root, err := filepath.Abs(cacheRoot)
	if err != nil {
		return nil, err
	}
	partial := filepath.Join(root, ".partial-"+transferID)
	data, err := os.ReadFile(filepath.Join(partial, stateFilename))
	if err != nil {
		return nil, err
	}
	var envelope stateEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	manifestData, err := json.Marshal(envelope.Manifest)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(manifestData)
	if envelope.SHA256 != hex.EncodeToString(sum[:]) || envelope.Manifest.TransferID != transferID {
		return nil, errors.New("传输恢复状态校验失败")
	}
	if err := envelope.Manifest.Validate(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(partial)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("传输临时目录无效")
	}
	return newReceiverState(root, partial, filepath.Join(root, transferID), envelope.Manifest), nil
}

func newReceiverState(root, partial, final string, manifest Manifest) *Receiver {
	receiver := &Receiver{root: root, manifest: manifest, partial: partial, final: final, entries: make(map[string]Entry, len(manifest.Entries))}
	for _, entry := range manifest.Entries {
		receiver.entries[entry.Path] = entry
	}
	return receiver
}

func (r *Receiver) ResumeOffset(relative string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, exists := r.entries[relative]
	if !exists || entry.Type != File {
		return 0, errors.New("文件不在传输清单中")
	}
	info, err := os.Stat(r.path(relative))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > entry.Size {
		return 0, errors.New("临时文件状态无效")
	}
	return info.Size(), nil
}

func (r *Receiver) Write(relative string, offset int64, reader io.Reader) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, exists := r.entries[relative]
	if !exists || entry.Type != File {
		return 0, errors.New("文件不在传输清单中")
	}
	if offset < 0 || offset > entry.Size {
		return 0, errors.New("文件偏移无效")
	}
	filename := r.path(relative)
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return 0, err
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if info.Size() != offset {
		return 0, fmt.Errorf("续传偏移不匹配: 本地 %d，请求 %d", info.Size(), offset)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}
	remaining := entry.Size - offset
	written, err := io.Copy(file, io.LimitReader(reader, remaining))
	if err != nil {
		return written, err
	}
	if err := file.Sync(); err != nil {
		return written, err
	}
	return written, nil
}

func (r *Receiver) CompleteFile(relative string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, exists := r.entries[relative]
	if !exists || entry.Type != File {
		return errors.New("文件不在传输清单中")
	}
	filename := r.path(relative)
	info, err := os.Stat(filename)
	if err != nil {
		return err
	}
	if info.Size() != entry.Size {
		return fmt.Errorf("文件长度不匹配: %s", relative)
	}
	hash, err := hashFile(filename)
	if err != nil {
		return err
	}
	if hash != entry.SHA256 {
		return fmt.Errorf("文件哈希不匹配: %s", relative)
	}
	if err := os.Chmod(filename, os.FileMode(entry.Mode)); err != nil {
		return err
	}
	return os.Chtimes(filename, entry.ModifiedAt, entry.ModifiedAt)
}

func (r *Receiver) Commit() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.manifest.Entries {
		filename := r.path(entry.Path)
		info, err := os.Stat(filename)
		if err != nil {
			return "", err
		}
		if entry.Type == Directory {
			if !info.IsDir() {
				return "", fmt.Errorf("目录类型不匹配: %s", entry.Path)
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Size() != entry.Size {
			return "", fmt.Errorf("文件尚未完成: %s", entry.Path)
		}
		hash, err := hashFile(filename)
		if err != nil || hash != entry.SHA256 {
			return "", fmt.Errorf("文件校验失败: %s", entry.Path)
		}
	}
	if err := os.Remove(filepath.Join(r.partial, stateFilename)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if _, err := os.Stat(r.final); err == nil {
		return "", errors.New("目标传输缓存已存在")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(r.partial, r.final); err != nil {
		return "", err
	}
	return r.final, nil
}

func (r *Receiver) Cancel() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return os.RemoveAll(r.partial)
}

func (r *Receiver) path(relative string) string {
	return filepath.Join(r.partial, filepath.FromSlash(relative))
}

func (r *Receiver) saveState() error {
	data, err := json.Marshal(r.manifest)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	envelope := stateEnvelope{Manifest: r.manifest, SHA256: hex.EncodeToString(sum[:])}
	data, err = json.Marshal(envelope)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.partial, stateFilename), data, 0o600)
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && relative != "" && relative[:1] != string(filepath.Separator)
}
