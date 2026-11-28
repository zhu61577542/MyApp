package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"myapp/internal/config"
	"myapp/internal/input"
)

func TestInputSettingsPersistBeforeUpdateAndWorkWhileDisconnected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input-settings.json")
	s := New(config.Default("test", config.RoleAgent), "local")
	if err := s.ConfigureInput(path); err != nil {
		t.Fatal(err)
	}
	want := input.Settings{PointerPercent: 70, ScrollPercent: 180}
	command := Command{Version: Version, ID: 1, Action: "input_settings", Input: &want}
	s.Bind(func(context.Context, Command) error { t.Fatal("速度命令依赖网络处理器"); return nil })
	if err := s.Apply(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if s.Sharing().InputSettings != want {
		t.Fatal("运行时未更新")
	}
	loaded, err := input.LoadSettings(path)
	if err != nil || loaded != want {
		t.Fatal(loaded, err)
	}
	s.Disconnected("断线")
	if s.Snapshot().InputSettings != want {
		t.Fatal("断线丢失速度")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	changed := input.Settings{PointerPercent: 90, ScrollPercent: 90}
	command.Input = &changed
	if err := s.Apply(context.Background(), command); err == nil {
		t.Fatal("保存失败仍报告成功")
	}
	if s.Sharing().InputSettings != want {
		t.Fatal("保存失败改变了实际倍率")
	}
	command.Input = nil
	if err := s.Apply(context.Background(), command); err == nil {
		t.Fatal("接受缺失设置")
	}
}
