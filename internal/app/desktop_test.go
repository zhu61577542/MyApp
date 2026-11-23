package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"myapp/internal/transfer"
)

func runDesktopLinks(t *testing.T, sender, receiver *Link) context.Context {
	return runDesktopLinksWithin(t, sender, receiver, 10*time.Second)
}

func runDesktopLinksWithin(t *testing.T, sender, receiver *Link, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	a, b := net.Pipe()
	c, d := net.Pipe()
	done := make(chan error, 2)
	go func() { done <- sender.RunChannels(ctx, a, c) }()
	go func() { done <- receiver.RunChannels(ctx, b, d) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("桌面会话退出超时")
			}
		}
	})
	return ctx
}

func TestDesktopControlRejectsWithoutEndingConnection(t *testing.T) {
	sender, _ := NewSplitLink(nil, nil)
	receiver, _ := NewSplitLink(&recordingInjector{}, nil)
	sender.EnableDesktop(Capabilities{Name: "controller"}, nil, nil)
	var mu sync.Mutex
	var ended bool
	receiver.EnableDesktop(Capabilities{Name: "agent", Control: true}, nil, func(_ context.Context, action string) error {
		if action == "begin" {
			return errors.New("权限未授权")
		}
		mu.Lock()
		ended = true
		mu.Unlock()
		return nil
	})
	ctx := runDesktopLinks(t, sender, receiver)
	if err := sender.RequestControl(ctx, "begin"); err == nil {
		t.Fatal("未授权仍允许控制")
	}
	if err := sender.RequestControl(ctx, "end"); err != nil {
		t.Fatalf("拒绝输入后连接不可用: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !ended {
		t.Fatal("未处理结束控制请求")
	}
}

func TestRemoteStopNotificationKeepsConnectionUsable(t *testing.T) {
	a, _ := NewSplitLink(nil, nil)
	b, _ := NewSplitLink(nil, nil)
	a.EnableDesktop(Capabilities{}, nil, nil)
	b.EnableDesktop(Capabilities{Control: true}, nil, func(context.Context, string) error { return nil })
	stopped := make(chan struct{})
	a.SetControlEndedHandler(func() { close(stopped) })
	ctx := runDesktopLinks(t, a, b)
	if err := a.RequestControl(ctx, "begin"); err != nil {
		t.Fatal(err)
	}
	if err := b.NotifyControlEnded(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("未收到对端停止控制通知")
	}
	if err := a.RequestControl(ctx, "begin"); err != nil {
		t.Fatalf("停止输入破坏了共享连接: %v", err)
	}
}

func TestTransferSuccessWaitsForPublication(t *testing.T) {
	sender, _ := NewSplitLink(nil, nil)
	receiver, _ := NewSplitLink(nil, nil)
	sender.EnableDesktop(Capabilities{FileResults: true}, nil, nil)
	receiver.EnableDesktop(Capabilities{FileResults: true}, nil, nil)
	var mu sync.Mutex
	var finalState string
	sender.ObserveTransfers(func(e TransferEvent, _ func(context.Context) error) { mu.Lock(); finalState = e.State; mu.Unlock() }, nil)
	receiver.ObserveTransfers(nil, nil)
	publishing := make(chan struct{})
	unblock := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(unblock) }) })
	if err := receiver.EnableTransfers(t.TempDir(), func(ctx context.Context, _ []string) error {
		close(publishing)
		select {
		case <-unblock:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}); err != nil {
		t.Fatal(err)
	}
	ctx := runDesktopLinks(t, sender, receiver)
	file := filepath.Join(t.TempDir(), "中文.txt")
	if err := os.WriteFile(file, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sender.SendFiles(ctx, "publication", []transfer.Source{{Path: file}}) }()
	select {
	case <-publishing:
	case <-ctx.Done():
		t.Fatal("未进入发布阶段")
	}
	mu.Lock()
	state := finalState
	mu.Unlock()
	if state == "completed" {
		t.Fatal("发布尚未成功就报告完成")
	}
	select {
	case err := <-done:
		t.Fatalf("发布前任务结束: %v", err)
	default:
	}
	once.Do(func() { close(unblock) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if finalState != "completed" {
		t.Fatalf("状态=%s", finalState)
	}
}

func TestTransferPublicationFailureIsNotSuccess(t *testing.T) {
	sender, _ := NewSplitLink(nil, nil)
	receiver, _ := NewSplitLink(nil, nil)
	sender.EnableDesktop(Capabilities{FileResults: true}, nil, nil)
	receiver.EnableDesktop(Capabilities{FileResults: true}, nil, nil)
	sender.ObserveTransfers(nil, nil)
	receiver.ObserveTransfers(nil, nil)
	if err := receiver.EnableTransfers(t.TempDir(), func(context.Context, []string) error { return errors.New("clipboard unavailable") }); err != nil {
		t.Fatal(err)
	}
	ctx := runDesktopLinks(t, sender, receiver)
	file := filepath.Join(t.TempDir(), "a")
	os.WriteFile(file, []byte("a"), 0600)
	if err := sender.SendFiles(ctx, "failed-publication", []transfer.Source{{Path: file}}); !errors.Is(err, ErrTransferTask) {
		t.Fatalf("错误=%v", err)
	}
}

func TestIncomingCancelIsAcknowledged(t *testing.T) {
	sender, _ := NewSplitLink(nil, nil)
	receiver, _ := NewSplitLink(nil, nil)
	sender.EnableDesktop(Capabilities{FileResults: true}, nil, nil)
	receiver.EnableDesktop(Capabilities{FileResults: true}, nil, nil)
	sender.ObserveTransfers(nil, nil)
	cancelResult := make(chan error, 1)
	var once sync.Once
	receiver.ObserveTransfers(func(e TransferEvent, cancel func(context.Context) error) {
		if e.State == "transferring" {
			once.Do(func() {
				go func() {
					ctx, done := context.WithTimeout(context.Background(), time.Second)
					defer done()
					cancelResult <- cancel(ctx)
				}()
			})
		}
	}, nil)
	if err := receiver.EnableTransfers(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	ctx := runDesktopLinks(t, sender, receiver)
	file := filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(file, bytes.Repeat([]byte("data"), 2<<20), 0600); err != nil {
		t.Fatal(err)
	}
	if err := sender.SendFiles(ctx, "cancelled", []transfer.Source{{Path: file}}); !errors.Is(err, ErrTransferTask) {
		t.Fatalf("取消没有结束源任务: %v", err)
	}
	if err := <-cancelResult; err != nil {
		t.Fatal(err)
	}
}

func TestBidirectionalTransfersDoNotBlockResults(t *testing.T) {
	started := time.Now()
	a, _ := NewSplitLink(nil, nil)
	b, _ := NewSplitLink(nil, nil)
	var progressMu sync.Mutex
	progress := make(map[string]string)
	for index, link := range []*Link{a, b} {
		link.EnableDesktop(Capabilities{FileResults: true}, nil, nil)
		link.ObserveTransfers(func(event TransferEvent, _ func(context.Context) error) {
			progressMu.Lock()
			defer progressMu.Unlock()
			progress[fmt.Sprintf("%d-%s", index, event.Direction)] = fmt.Sprintf("%s %d/%d error=%s", event.State, event.Bytes, event.Total, event.Error)
		}, nil)
		link.SetDisconnectHandler(func(err error) {
			progressMu.Lock()
			defer progressMu.Unlock()
			progress[fmt.Sprintf("%d-session", index)] = fmt.Sprint(err)
		})
		if err := link.EnableTransfers(t.TempDir(), nil); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(t.TempDir(), "同时发送.bin")
	if err := os.WriteFile(file, bytes.Repeat([]byte("data"), 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := runDesktopLinksWithin(t, a, b, 30*time.Second)
	done := make(chan error, 2)
	go func() { done <- a.SendFiles(ctx, "from-a", []transfer.Source{{Path: file}}) }()
	go func() { done <- b.SendFiles(ctx, "from-b", []transfer.Source{{Path: file}}) }()
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				progressMu.Lock()
				snapshot := fmt.Sprint(progress)
				progressMu.Unlock()
				t.Fatalf("双向传输失败: %v; %s", err, snapshot)
			}
		case <-ctx.Done():
			progressMu.Lock()
			snapshot := fmt.Sprint(progress)
			progressMu.Unlock()
			t.Fatalf("双向传输超时: %s", snapshot)
		}
	}
	t.Logf("双向各 4 MiB 完成，用时 %s", time.Since(started).Round(time.Millisecond))
}
