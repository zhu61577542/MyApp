package clipboard

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

const (
	TextMIME     = "text/plain;charset=utf-8"
	MaxTextBytes = 128 << 10
)

type Entry struct {
	Origin    string    `json:"origin"`
	Version   uint64    `json:"version"`
	MIME      string    `json:"mime"`
	Hash      string    `json:"hash"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

func NewEntry(origin string, version uint64, text string, now time.Time) (Entry, error) {
	entry := Entry{Origin: origin, Version: version, MIME: TextMIME, Text: text, CreatedAt: now.UTC()}
	entry.Hash = TextHash(text)
	if err := entry.Validate(); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (e Entry) Validate() error {
	if e.Origin == "" || e.Version == 0 {
		return errors.New("剪贴板来源和版本不能为空")
	}
	if e.MIME != TextMIME {
		return errors.New("剪贴板 MIME 类型无效")
	}
	if len(e.Text) > MaxTextBytes {
		return errors.New("剪贴板文字超过 128 KiB")
	}
	if e.Hash != TextHash(e.Text) {
		return errors.New("剪贴板文字哈希不匹配")
	}
	if e.CreatedAt.IsZero() {
		return errors.New("剪贴板时间不能为空")
	}
	return nil
}

func TextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
