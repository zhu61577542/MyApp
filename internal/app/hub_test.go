package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"myapp/internal/clipboard"
	"myapp/internal/config"
	"myapp/internal/device"
	common "myapp/internal/input"
	"myapp/internal/transfer"
)

func TestHubThreeNodesRelayAndSwitch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hub, err := NewHub(device.Device{ID: "main", Name: "main", Role: config.RoleController}, 3)
	if err != nil {
		t.Fatal(err)
	}
	texts := []chan clipboard.Entry{make(chan clipboard.Entry, 4), make(chan clipboard.Entry, 4)}
	files := []chan []string{make(chan []string, 4), make(chan []string, 4)}
	agents := make([]*Link, 2)
	injectors := []*recordingInjector{{}, {}}
	done := make(chan error, 6)
	for i, id := range []string{"one", "two"} {
		controller, _ := NewLink(nil, nil)
		agent, _ := NewLink(injectors[i], nil)
		agents[i] = agent
		controller.SetTextReceiver(func(ctx context.Context, entry clipboard.Entry) error {
			return hub.PublishText(ctx, id, entry)
		})
		agent.SetTextReceiver(func(_ context.Context, entry clipboard.Entry) error {
			texts[i] <- entry
			return nil
		})
		if err := controller.EnableTransfers(t.TempDir(), func(ctx context.Context, paths []string) error {
			return hub.PublishFiles(ctx, id, "relay-"+id, paths)
		}); err != nil {
			t.Fatal(err)
		}
		if err := agent.EnableTransfers(t.TempDir(), func(_ context.Context, paths []string) error {
			files[i] <- paths
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := hub.AddPeer(device.Device{ID: id, Name: id, Role: config.RoleAgent, Online: true}, controller); err != nil {
			t.Fatal(err)
		}
		client, server := net.Pipe()
		go func() { done <- controller.Run(ctx, client) }()
		go func() { done <- agent.Run(ctx, server) }()
		go func() { done <- hub.RunRelay(ctx, id) }()
	}
	defer func() {
		cancel()
		for range 6 {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("会话任务未退出")
			}
		}
	}()
	extra, _ := NewLink(nil, nil)
	if err := hub.AddPeer(device.Device{ID: "three", Name: "three", Role: config.RoleAgent}, extra); !errors.Is(err, device.ErrCapacity) {
		t.Fatalf("未拒绝第四台设备: %v", err)
	}
	if err := hub.Activate(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := hub.SendInput(ctx, common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}); err != nil {
		t.Fatal(err)
	}
	if err := hub.Activate(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := hub.SendInput(ctx, common.Event{Kind: common.KeyDown, Sequence: 2, Code: 5}); err != nil {
		t.Fatal(err)
	}
	for from, origin := range []string{"one", "two"} {
		entry, _ := clipboard.NewEntry(origin, 1, "来自"+origin, time.Now())
		if err := agents[from].PublishClipboard(ctx, entry); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-texts[1-from]:
			if got.Origin != origin || got.Text != entry.Text {
				t.Fatalf("转发内容错误: %+v", got)
			}
		case <-ctx.Done():
			t.Fatal("子电脑之间文字未送达")
		}
	}
	source := filepath.Join(t.TempDir(), "中文.txt")
	if err := os.WriteFile(source, []byte("跨子电脑文件"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := agents[0].SendFiles(ctx, "source", []transfer.Source{{Path: source}}); err != nil {
		t.Fatal(err)
	}
	select {
	case paths := <-files[1]:
		data, err := os.ReadFile(paths[0])
		if err != nil || string(data) != "跨子电脑文件" {
			t.Fatalf("转发文件错误: %q %v", data, err)
		}
	case <-ctx.Done():
		t.Fatal("子电脑之间文件未送达")
	}
	for _, channel := range texts {
		select {
		case <-channel:
			t.Error("文字被回传给来源")
		default:
		}
	}
	select {
	case <-files[0]:
		t.Error("文件被回传给来源")
	default:
	}
	for i, injector := range injectors {
		injector.mu.Lock()
		if len(injector.events) != 1 || injector.events[0].Code != uint16(4+i) {
			t.Errorf("目标 %d 收到错误输入: %+v", i, injector.events)
		}
		if i == 0 && injector.releases == 0 {
			t.Error("切换未释放旧目标")
		}
		injector.mu.Unlock()
	}
}
