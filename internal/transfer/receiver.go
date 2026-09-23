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
	"strings"
	"sync"
)

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
			dataRoot, err := receiver.openData()
			if err != nil {
				return nil, err
			}
			err = dataRoot.MkdirAll(filepath.FromSlash(entry.Path), 0700)
			dataRoot.Close()
			if err != nil {
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
	cache, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer cache.Close()
	data, err := cache.ReadFile(".state-" + transferID + ".json")
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
	info, err := cache.Lstat(filepath.Base(partial))
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
	dataRoot, err := r.openData()
	if err != nil {
		return 0, err
	}
	defer dataRoot.Close()
	if err := rejectLinks(dataRoot, relative); err != nil {
		return 0, err
	}
	info, err := dataRoot.Stat(filepath.FromSlash(relative))
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
	dataRoot, err := r.openData()
	if err != nil {
		return 0, err
	}
	defer dataRoot.Close()
	if err := rejectLinks(dataRoot, relative); err != nil {
		return 0, err
	}
	filename := filepath.FromSlash(relative)
	if err := dataRoot.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return 0, err
	}
	file, err := dataRoot.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0o600)
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
	if !info.Mode().IsRegular() {
		return 0, errors.New("接收目标不是普通文件")
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
	dataRoot, err := r.openData()
	if err != nil {
		return err
	}
	defer dataRoot.Close()
	if err := rejectLinks(dataRoot, relative); err != nil {
		return err
	}
	filename := filepath.FromSlash(relative)
	file, err := dataRoot.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != entry.Size {
		return fmt.Errorf("文件长度不匹配: %s", relative)
	}
	hash, err := hashReader(file)
	if err != nil {
		return err
	}
	if hash != entry.SHA256 {
		return fmt.Errorf("文件哈希不匹配: %s", relative)
	}
	if err := file.Chmod(os.FileMode(entry.Mode) & 0777); err != nil {
		return err
	}
	return nil
}

func (r *Receiver) Commit() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dataRoot, err := r.openData()
	if err != nil {
		return "", err
	}
	defer dataRoot.Close()
	for _, entry := range r.manifest.Entries {
		if err := rejectLinks(dataRoot, entry.Path); err != nil {
			return "", err
		}
		filename := filepath.FromSlash(entry.Path)
		info, err := dataRoot.Stat(filename)
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
		file, err := dataRoot.Open(filename)
		if err != nil {
			return "", err
		}
		hash, err := hashReader(file)
		file.Close()
		if err != nil || hash != entry.SHA256 {
			return "", fmt.Errorf("文件校验失败: %s", entry.Path)
		}
	}
	cache, err := os.OpenRoot(r.root)
	if err != nil {
		return "", err
	}
	defer cache.Close()
	if _, err := cache.Lstat(filepath.Base(r.final)); err == nil {
		return "", errors.New("目标传输缓存已存在")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	dataRoot.Close()
	if err := cache.Rename(filepath.Base(r.partial), filepath.Base(r.final)); err != nil {
		return "", err
	}
	_ = cache.Remove(".state-" + r.manifest.TransferID + ".json")
	return r.final, nil
}

func (r *Receiver) Cancel() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cache, err := os.OpenRoot(r.root)
	if err != nil {
		return err
	}
	defer cache.Close()
	if err := cache.RemoveAll(filepath.Base(r.partial)); err != nil {
		return err
	}
	err = cache.Remove(".state-" + r.manifest.TransferID + ".json")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
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
	cache, err := os.OpenRoot(r.root)
	if err != nil {
		return err
	}
	defer cache.Close()
	file, err := cache.OpenFile(".state-"+r.manifest.TransferID+".json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Sync(), file.Close())
}

func (r *Receiver) openData() (*os.Root, error) {
	cache, err := os.OpenRoot(r.root)
	if err != nil {
		return nil, err
	}
	defer cache.Close()
	name := filepath.Base(r.partial)
	info, err := cache.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("缓存数据目录无效")
	}
	return cache.OpenRoot(name)
}

func rejectLinks(root *os.Root, relative string) error {
	if err := ValidateRelativePath(relative); err != nil {
		return err
	}
	var current string
	for _, component := range strings.Split(relative, "/") {
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("接收路径包含符号链接")
		}
	}
	return nil
}

func hashReader(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func within(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != "." && filepath.IsLocal(relative)
}
