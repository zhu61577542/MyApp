package app

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	common "myapp/internal/input"
	"myapp/internal/transfer"
)

type recordingInjector struct {
	mu       sync.Mutex
	events   []common.Event
	releases int
}

func TestLinkTransfersDirectoryAndPublishesTopLevelPath(t *testing.T) {
	sourceRoot := t.TempDir()
	sourceDirectory := filepath.Join(sourceRoot, "共享目录")
	if err := os.Mkdir(sourceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDirectory, "内容.txt"), []byte("hello 文件"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDirectory, "empty.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sender, _ := NewLink(nil, nil)
	receiver, _ := NewLink(nil, nil)
	ready := make(chan []string, 1)
	cache := t.TempDir()
	if err := receiver.EnableTransfers(cache, func(_ context.Context, paths []string) error {
		ready <- paths
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sender.Run(ctx, client)
	go receiver.Run(ctx, server)
	if err := sender.SendFiles(ctx, "files-1", []transfer.Source{{Path: sourceDirectory}}); err != nil {
		t.Fatal(err)
	}
	select {
	case paths := <-ready:
		if len(paths) != 1 {
			t.Fatalf("发布路径=%v", paths)
		}
		content, err := os.ReadFile(filepath.Join(paths[0], "内容.txt"))
		if err != nil || string(content) != "hello 文件" {
			t.Fatalf("接收文件=%q err=%v", content, err)
		}
		if info, err := os.Stat(filepath.Join(paths[0], "empty.txt")); err != nil || info.Size() != 0 {
			t.Fatalf("空文件未正确接收: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("文件传输超时")
	}
	_ = client.Close()
	_ = server.Close()
}

func (i *recordingInjector) Inject(event common.Event) error {
	i.mu.Lock()
	i.events = append(i.events, event)
	i.mu.Unlock()
	return nil
}

func (i *recordingInjector) ReleaseAll() error {
	i.mu.Lock()
	i.releases++
	i.mu.Unlock()
	return nil
}

func (i *recordingInjector) Close() error { return nil }

func TestLinkCancellationClosesBlockedConnection(t *testing.T) {
	injector := &recordingInjector{}
	link, _ := NewLink(injector, nil)
	client, server := net.Pipe()
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- link.Run(ctx, client) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消后仍阻塞在连接读写")
	}
	if _, err := server.Write([]byte{1}); err == nil {
		t.Fatal("取消后连接仍可写入")
	}
	injector.mu.Lock()
	defer injector.mu.Unlock()
	if injector.releases != 1 {
		t.Fatalf("释放次数 = %d", injector.releases)
	}
}

func TestLinkDeliversInputAndReleasesOnDisconnect(t *testing.T) {
	injector := &recordingInjector{}
	sender, _ := NewLink(nil, nil)
	receiver, _ := NewLink(injector, nil)
	client, server := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	go func() { done <- sender.Run(ctx, client) }()
	go func() { done <- receiver.Run(ctx, server) }()
	event := common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}
	if err := sender.SendInput(ctx, event); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		injector.mu.Lock()
		count := len(injector.events)
		injector.mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("输入事件未到达")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	_ = client.Close()
	_ = server.Close()
	for range 2 {
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, io.EOF) {
			t.Fatalf("Run() = %v", err)
		}
	}
	injector.mu.Lock()
	defer injector.mu.Unlock()
	if injector.events[0] != event || injector.releases == 0 {
		t.Fatalf("注入记录=%+v releases=%d", injector.events, injector.releases)
	}
}
