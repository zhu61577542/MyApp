package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"myapp/internal/config"
	"myapp/internal/input"
)

func TestInputSettingsSurviveFreshProcessAndDefaultReset(t *testing.T) {
	const pathKey = "MYAPP_SETTINGS_RESTART_TEST_PATH"
	if path := os.Getenv(pathKey); path != "" {
		pointer, err := strconv.Atoi(os.Getenv("MYAPP_SETTINGS_RESTART_TEST_POINTER"))
		if err != nil {
			t.Fatal(err)
		}
		scroll, err := strconv.Atoi(os.Getenv("MYAPP_SETTINGS_RESTART_TEST_SCROLL"))
		if err != nil {
			t.Fatal(err)
		}
		state := New(config.Default("restart-test", config.RoleAgent), "local")
		if err := state.ConfigureInput(path); err != nil {
			t.Fatal(err)
		}
		want := input.Settings{PointerPercent: pointer, ScrollPercent: scroll}
		if state.Sharing().InputSettings != want {
			t.Fatal("新进程未读回保存值", state.Sharing().InputSettings, want)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "input-settings.json")
	state := New(config.Default("settings-test", config.RoleAgent), "local")
	if err := state.ConfigureInput(path); err != nil {
		t.Fatal(err)
	}
	for n, want := range []input.Settings{{PointerPercent: 70, ScrollPercent: 150}, input.DefaultSettings(), {PointerPercent: 60, ScrollPercent: 120}} {
		if err := state.Apply(context.Background(), Command{Version: Version, ID: uint64(n + 1), Action: "input_settings", Input: &want}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInputSettingsSurviveFreshProcessAndDefaultReset$", "-test.v")
		command.Env = append(os.Environ(), pathKey+"="+path, "MYAPP_SETTINGS_RESTART_TEST_POINTER="+strconv.Itoa(want.PointerPercent), "MYAPP_SETTINGS_RESTART_TEST_SCROLL="+strconv.Itoa(want.ScrollPercent))
		output, err := command.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("跨进程保存/恢复默认失败: %v\n%s", err, output)
		}
	}
}
