package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestHandshakeTimeoutAndCancellation(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancel"}[cancelEarly], func(t *testing.T) {
			local, remote := net.Pipe()
			defer local.Close()
			defer remote.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				done <- withHandshakeDeadline(ctx, local, 50*time.Millisecond, func() error {
					close(started)
					_, err := local.Read(make([]byte, 1))
					return err
				})
			}()
			<-started
			if cancelEarly {
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("无响应握手未报错")
				}
			case <-time.After(time.Second):
				t.Fatal("握手未退出")
			}
		})
	}
}

func TestSessionWaitsForCleanup(t *testing.T) {
	want := errors.New("connection lost")
	cleaning := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runSession(context.Background(),
			func(context.Context) error { return want },
			func(ctx context.Context) error {
				<-ctx.Done()
				close(cleaning)
				<-release
				return ctx.Err()
			})
	}()
	select {
	case <-cleaning:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("未取消其他会话任务")
	}
	select {
	case <-done:
		close(release)
		t.Fatal("资源清理完成前返回")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("原始错误丢失: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("清理后未返回")
	}
}

func TestSessionParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runSession(ctx, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误: %v", err)
	}
}
