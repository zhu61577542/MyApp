package main

import (
	"bytes"
	"encoding/json"
	"io"
	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/identity"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func desktopServiceArgs(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	local, err := identity.Generate("测试电脑", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	idDir := filepath.Join(root, "identity")
	if err := identity.SaveNew(idDir, local); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	cfg := config.Default("测试电脑", config.RoleAgent)
	cfg.ListenAddress = address
	path := filepath.Join(root, "config.json")
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	return []string{"--config", path, "--identity-dir", idDir, "--trust-dir", filepath.Join(root, "trust")}
}

func TestDesktopServiceEOFStopsWithoutGUI(t *testing.T) {
	args := desktopServiceArgs(t)
	done := make(chan error, 1)
	go func() { done <- runDesktopService(args, bytes.NewReader(nil), io.Discard, io.Discard) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("状态管道EOF未停止服务")
	}
}

func TestDesktopServiceAcknowledgesPauseBeforeStop(t *testing.T) {
	args := desktopServiceArgs(t)
	input, send := io.Pipe()
	output, publish := io.Pipe()
	defer input.Close()
	defer send.Close()
	defer output.Close()
	defer publish.Close()
	done := make(chan error, 1)
	go func() { done <- runDesktopService(args, input, publish, io.Discard); publish.Close() }()
	messages := make(chan desktop.Message, 16)
	go func() {
		decoder := json.NewDecoder(output)
		for {
			var message desktop.Message
			if decoder.Decode(&message) != nil {
				return
			}
			messages <- message
		}
	}()
	deadline := time.After(3 * time.Second)
	wait := func(match func(desktop.Message) bool) desktop.Message {
		t.Helper()
		for {
			select {
			case message := <-messages:
				if match(message) {
					return message
				}
			case <-deadline:
				t.Fatal("等待桌面状态超时")
				return desktop.Message{}
			}
		}
	}
	wait(func(message desktop.Message) bool { return message.Snapshot.Phase == "listening" })
	encoder := json.NewEncoder(send)
	if err := encoder.Encode(desktop.Command{Version: desktop.Version, ID: 1, Action: "prepare_stop"}); err != nil {
		t.Fatal(err)
	}
	reply := wait(func(message desktop.Message) bool { return message.RequestID == 1 })
	if reply.Error != "" || !reply.Snapshot.Paused {
		t.Fatalf("停止准备没有返回已生效状态: %+v", reply)
	}
	if err := encoder.Encode(desktop.Command{Version: desktop.Version, ID: 2, Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-deadline:
		t.Fatal("桌面服务停止超时")
	}
}
