package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	common "myapp/internal/input"
	"myapp/internal/protocol"
	"myapp/internal/transfer"
	"myapp/internal/transport"
)

func TestSplitHeartbeatReleasesWithinOneSecond(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	injector := &recordingInjector{}
	link, _ := NewSplitLink(injector, nil)
	client, server := net.Pipe()
	bulkClient, bulkServer := net.Pipe()
	defer client.Close()
	defer bulkClient.Close()
	go io.Copy(io.Discard, client)
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- link.RunChannels(ctx, server, bulkServer) }()
	var payload bytes.Buffer
	if err := (common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}).Encode(&payload); err != nil {
		t.Fatal(err)
	}
	if err := (protocol.Frame{Type: protocol.TypeInput, Payload: payload.Bytes()}).Encode(client, 0); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, transport.ErrHeartbeatTimeout) {
			t.Fatalf("断网结束原因错误: %v", err)
		}
		if time.Since(start) >= time.Second {
			t.Fatal("断网恢复超过一秒")
		}
		injector.mu.Lock()
		defer injector.mu.Unlock()
		if injector.releases == 0 || len(injector.events) != 1 {
			t.Fatal("断网未释放已经按下的键")
		}
	case <-time.After(time.Second):
		t.Fatal("静默断网未及时检测")
	}
}

func TestSplitChannelKeepsInputAliveDuringBlockedFileWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sender, _ := NewSplitLink(nil, nil)
	injector := &recordingInjector{}
	receiver, _ := NewSplitLink(injector, nil)
	blocked, unblock := make(chan struct{}), make(chan struct{})
	if err := receiver.EnableTransfers(t.TempDir(), func(context.Context, []string) error {
		close(blocked)
		<-unblock
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rtClient, rtServer := net.Pipe()
	bulkClient, bulkServer := net.Pipe()
	done := make(chan error, 2)
	go func() { done <- sender.RunChannels(ctx, rtClient, bulkClient) }()
	go func() { done <- receiver.RunChannels(ctx, rtServer, bulkServer) }()
	defer func() {
		cancel()
		close(unblock)
		for range 2 {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("拆分通道未退出")
			}
		}
	}()
	source := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(source, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendFiles(ctx, "blocked", []transfer.Source{{Path: source}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("未进入文件处理")
	}
	if err := sender.SendInput(ctx, common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}); err != nil {
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
			t.Fatal("文件阻塞了输入")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(900 * time.Millisecond)
	injector.mu.Lock()
	released := injector.releases
	injector.mu.Unlock()
	if released != 0 {
		t.Fatal("批量任务阻塞造成实时心跳误超时")
	}
	cancel()
	deadline = time.Now().Add(time.Second)
	for {
		injector.mu.Lock()
		released = injector.releases
		injector.mu.Unlock()
		if released > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("按键释放等待了批量任务完成")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBulkChannelRejectsInput(t *testing.T) {
	injector := &recordingInjector{}
	link, _ := NewSplitLink(injector, nil)
	var payload, wire bytes.Buffer
	if err := (common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}).Encode(&payload); err != nil {
		t.Fatal(err)
	}
	if err := (protocol.Frame{Type: protocol.TypeInput, Payload: payload.Bytes()}).Encode(&wire, 0); err != nil {
		t.Fatal(err)
	}
	monitor, _ := transport.NewHeartbeatMonitor(time.Second, time.Now())
	if err := link.readChannel(context.Background(), &wire, monitor, transport.ChannelBulk); err == nil || len(injector.events) != 0 {
		t.Fatalf("批量通道接受输入: %v", err)
	}
}
