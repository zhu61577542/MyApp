package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputSettingsDefaultRoundTripAndFailurePreservesOld(t *testing.T) {
	path := SettingsPath(filepath.Join(t.TempDir(), "config.json"))
	if got, err := LoadSettings(path); err != nil || got != DefaultSettings() {
		t.Fatal(got, err)
	}
	want := Settings{PointerPercent: 70, ScrollPercent: 150}
	if err := SaveSettings(path, want); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(path, Settings{}); err == nil {
		t.Fatal("非法配置覆盖成功")
	}
	if got, err := LoadSettings(path); err != nil || got != want {
		t.Fatal("未保留原偏好", got, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("残留临时文件", entries, err)
	}
	if err := os.Mkdir(path+".dir", 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(path+".dir", want); err == nil {
		t.Fatal("无法替换目标却报告成功")
	}
}

func TestInputSettingsRejectCorruptAndUnsupportedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, data := range []string{
		`{}`, `null`, `{"version":2,"pointer_percent":100,"scroll_percent":100}`,
		`{"version":1,"pointer_percent":0,"scroll_percent":100}`,
		`{"version":1,"pointer_percent":100,"scroll_percent":401}`,
		`{"version":1,"pointer_percent":100,"scroll_percent":100,"extra":true}`,
		`{"version":1,"pointer_percent":100,"scroll_percent":100} {}`,
		`{"version":1,"pointer_percent":100,"scroll_percent":100}` + strings.Repeat(" ", 4096),
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSettings(path); err == nil {
			t.Fatal("接受损坏或不兼容配置")
		}
	}
}
