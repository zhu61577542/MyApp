package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Default("主电脑", RoleController)
	if err := SaveNew(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("配置不一致: got=%+v want=%+v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("配置权限=%o", info.Mode().Perm())
	}
	if err := SaveNew(path, want); !os.IsExist(err) {
		t.Fatalf("重复初始化应失败: %v", err)
	}
}

func TestLoadRejectsUnknownAndTrailingData(t *testing.T) {
	base := `{"schema_version":1,"device_name":"pc","role":"agent","listen_address":":24800","max_devices":3,"switch":{"mode":"edge","edge_delay_ms":0},"clipboard":{"text_enabled":true},"files":{"enabled":true,"cache_directory":"cache"}`
	for name, body := range map[string]string{
		"unknown":  base + `,"unknown":true}`,
		"trailing": base + `} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("无效配置未被拒绝")
			}
		})
	}
}

func TestValidateReportsMultipleProblems(t *testing.T) {
	cfg := Default(" pc ", Role("root"))
	cfg.MaxDevices = 4
	cfg.Switch.EdgeDelayMS = 3000
	err := cfg.Validate()
	if err == nil {
		t.Fatal("应返回错误")
	}
	for _, text := range []string{"device_name", "role", "max_devices", "edge_delay_ms"} {
		if !strings.Contains(err.Error(), text) {
			t.Fatalf("缺少错误 %q: %v", text, err)
		}
	}
}
