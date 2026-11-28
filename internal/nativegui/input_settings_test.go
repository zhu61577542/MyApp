//go:build gui

package nativegui

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"myapp/internal/desktop"
	"myapp/internal/input"
)

func TestInputSpeedSaveOfflineAndRunningServiceAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "input-settings.json")
	m := &serviceManager{}
	want := input.Settings{PointerPercent: 70, ScrollPercent: 100}
	if err := m.saveInputSettings(ctx, path, want); err != nil {
		t.Fatal(err)
	}
	got, err := input.LoadSettings(path)
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	m.cmd = &exec.Cmd{}
	m.input = writer
	m.pending = make(map[uint64]chan desktop.Message)
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		var command desktop.Command
		if err := json.NewDecoder(reader).Decode(&command); err != nil {
			t.Error(err)
			return
		}
		if command.Action != "input_settings" || command.Input == nil || *command.Input != want {
			t.Error("界面没有传递独立倍率")
		}
		m.mu.Lock()
		reply := m.pending[command.ID]
		m.mu.Unlock()
		reply <- desktop.Message{Version: desktop.Version, RequestID: command.ID, Error: "测试保存失败"}
	}()
	if err := m.saveInputSettings(ctx, path, want); err == nil || err.Error() != "测试保存失败" {
		t.Fatal("忽略了服务保存失败", err)
	}
	<-checked
}
