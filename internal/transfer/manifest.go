package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type EntryType string

const (
	File      EntryType = "file"
	Directory EntryType = "directory"
)

type Entry struct {
	Path       string    `json:"path"`
	Type       EntryType `json:"type"`
	Size       int64     `json:"size"`
	Mode       uint32    `json:"mode"`
	ModifiedAt time.Time `json:"modified_at"`
	SHA256     string    `json:"sha256,omitempty"`
}

type Manifest struct {
	TransferID string    `json:"transfer_id"`
	Entries    []Entry   `json:"entries"`
	CreatedAt  time.Time `json:"created_at"`
}

type Source struct {
	Path string
	Name string
}

func BuildManifest(transferID string, sources []Source, now time.Time) (Manifest, error) {
	if transferID == "" || len(sources) == 0 {
		return Manifest{}, errors.New("传输 ID 和源文件不能为空")
	}
	manifest := Manifest{TransferID: transferID, CreatedAt: now.UTC()}
	seen := make(map[string]struct{})
	for _, source := range sources {
		_, err := os.Lstat(source.Path)
		if err != nil {
			return Manifest{}, err
		}
		name := source.Name
		if name == "" {
			name = filepath.Base(source.Path)
		}
		if err := ValidateRelativePath(filepath.ToSlash(name)); err != nil {
			return Manifest{}, err
		}
		err = filepath.WalkDir(source.Path, func(current string, item fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := os.Lstat(current)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
				return fmt.Errorf("拒绝特殊文件: %s", current)
			}
			relative, err := filepath.Rel(source.Path, current)
			if err != nil {
				return err
			}
			target := filepath.ToSlash(name)
			if relative != "." {
				target = path.Join(target, filepath.ToSlash(relative))
			}
			if err := ValidateRelativePath(target); err != nil {
				return err
			}
			if _, exists := seen[target]; exists {
				return fmt.Errorf("清单路径重复: %s", target)
			}
			seen[target] = struct{}{}
			entry := Entry{Path: target, Size: info.Size(), Mode: uint32(info.Mode().Perm()), ModifiedAt: info.ModTime().UTC()}
			if info.IsDir() {
				entry.Type, entry.Size = Directory, 0
			} else {
				entry.Type = File
				hash, err := hashFile(current)
				if err != nil {
					return err
				}
				entry.SHA256 = hash
			}
			manifest.Entries = append(manifest.Entries, entry)
			return nil
		})
		if err != nil {
			return Manifest{}, err
		}
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if !validTransferID(m.TransferID) || m.CreatedAt.IsZero() || len(m.Entries) == 0 {
		return errors.New("文件清单不完整")
	}
	seen := make(map[string]struct{}, len(m.Entries))
	for _, entry := range m.Entries {
		if err := ValidateRelativePath(entry.Path); err != nil {
			return err
		}
		if _, exists := seen[entry.Path]; exists {
			return fmt.Errorf("清单路径重复: %s", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		if entry.Size < 0 || entry.ModifiedAt.IsZero() {
			return fmt.Errorf("清单条目无效: %s", entry.Path)
		}
		switch entry.Type {
		case Directory:
			if entry.Size != 0 || entry.SHA256 != "" {
				return fmt.Errorf("目录条目无效: %s", entry.Path)
			}
		case File:
			decoded, err := hex.DecodeString(entry.SHA256)
			if err != nil || len(decoded) != sha256.Size {
				return fmt.Errorf("文件哈希无效: %s", entry.Path)
			}
		default:
			return fmt.Errorf("条目类型无效: %s", entry.Path)
		}
	}
	return nil
}

func validTransferID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character < 'a' || character > 'z' {
			if character < 'A' || character > 'Z' {
				if character < '0' || character > '9' {
					if character != '-' && character != '_' {
						return false
					}
				}
			}
		}
	}
	return true
}

func ValidateRelativePath(value string) error {
	if value == "" || strings.ContainsRune(value, 0) || strings.Contains(value, "\\") {
		return errors.New("相对路径无效")
	}
	if strings.HasPrefix(value, "/") || path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("拒绝越界路径: %s", value)
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("拒绝越界路径: %s", value)
		}
	}
	return nil
}

func hashFile(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
