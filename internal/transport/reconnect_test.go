package transport

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestReconnectEventuallyRunsSession(t *testing.T) {
	backoff, _ := NewBackoff(time.Millisecond, 2*time.Millisecond)
	var attempts atomic.Int32
	dial := func(context.Context) (net.Conn, error) {
		if attempts.Add(1) < 3 {
			return nil, errors.New("暂时离线")
		}
		client, server := net.Pipe()
		go server.Close()
		return client, nil
	}
	var sessions atomic.Int32
	err := RunReconnect(context.Background(), dial, func(context.Context, net.Conn) error {
		sessions.Add(1)
		return nil
	}, backoff, nil)
	if err != nil || attempts.Load() != 3 || sessions.Load() != 1 {
		t.Fatalf("重连结果错误: attempts=%d sessions=%d err=%v", attempts.Load(), sessions.Load(), err)
	}
}

func TestReconnectStopsOnContext(t *testing.T) {
	backoff, _ := NewBackoff(time.Second, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RunReconnect(ctx, func(context.Context) (net.Conn, error) {
		return nil, errors.New("不应调用")
	}, func(context.Context, net.Conn) error { return nil }, backoff, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误=%v", err)
	}
}
