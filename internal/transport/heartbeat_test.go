package transport

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"myapp/internal/protocol"
)

func TestHeartbeatMonitorAndFrame(t *testing.T) {
	now := time.Unix(1700000000, 123)
	monitor, err := NewHeartbeatMonitor(3*time.Second, now)
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Expired(now.Add(3*time.Second)) || !monitor.Expired(now.Add(3*time.Second+time.Nanosecond)) {
		t.Fatal("心跳超时边界错误")
	}
	monitor.Observe(now.Add(2 * time.Second))
	if monitor.Expired(now.Add(4 * time.Second)) {
		t.Fatal("新心跳没有延长有效期")
	}
	frame := NewHeartbeat(9, now)
	parsed, err := ParseHeartbeat(frame)
	if err != nil || !parsed.Equal(now) {
		t.Fatalf("心跳解析错误: time=%v err=%v", parsed, err)
	}
	if _, err := ParseHeartbeat(protocol.Frame{Type: protocol.TypeHeartbeat}); !errors.Is(err, ErrHeartbeatPayload) {
		t.Fatalf("空心跳错误=%v", err)
	}
}

func TestRunHeartbeatTimesOutWithoutObservation(t *testing.T) {
	now := time.Now()
	monitor, _ := NewHeartbeatMonitor(15*time.Millisecond, now)
	var sent atomic.Int32
	err := RunHeartbeat(context.Background(), monitor, 5*time.Millisecond, func(protocol.Frame) error {
		sent.Add(1)
		return nil
	})
	if !errors.Is(err, ErrHeartbeatTimeout) || sent.Load() == 0 {
		t.Fatalf("心跳结果错误: sent=%d err=%v", sent.Load(), err)
	}
}
