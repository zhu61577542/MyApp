package input

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Settings struct {
	PointerPercent int `json:"pointer_percent"`
	ScrollPercent  int `json:"scroll_percent"`
}

func DefaultSettings() Settings { return Settings{PointerPercent: 100, ScrollPercent: 100} }

func (s Settings) Validate() error {
	if s.PointerPercent < 10 || s.PointerPercent > 400 || s.ScrollPercent < 10 || s.ScrollPercent > 400 {
		return errors.New("移动和滚动速度必须在10%至400%之间")
	}
	return nil
}

func SettingsPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "input-settings.json")
}

type settingsFile struct {
	Version int `json:"version"`
	Settings
}

func LoadSettings(path string) (Settings, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Settings{}, err
	}
	if info.Size() > 4096 {
		return Settings{}, errors.New("键鼠速度设置文件过大")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	var stored settingsFile
	if err := decoder.Decode(&stored); err != nil {
		return Settings{}, fmt.Errorf("读取键鼠速度设置: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Settings{}, errors.New("键鼠速度设置包含多余内容")
	}
	if stored.Version != 1 {
		return Settings{}, errors.New("键鼠速度设置版本无效")
	}
	if err := stored.Settings.Validate(); err != nil {
		return Settings{}, err
	}
	return stored.Settings, nil
}

func SaveSettings(path string, settings Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".myapp-input-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		return errors.Join(err, file.Close())
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := errors.Join(encoder.Encode(settingsFile{Version: 1, Settings: settings}), file.Sync(), file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
