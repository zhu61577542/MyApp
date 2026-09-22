package transport

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoffCapsAndResets(t *testing.T) {
	backoff, err := NewBackoff(time.Second, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for index, expected := range want {
		if got := backoff.Next(); got != expected {
			t.Fatalf("第 %d 次退避=%s，期望=%s", index, got, expected)
		}
	}
	backoff.Reset()
	if got := backoff.Next(); got != time.Second {
		t.Fatalf("重置后退避=%s", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backoff.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消等待错误=%v", err)
	}
}
