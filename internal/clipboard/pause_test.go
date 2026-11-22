package clipboard

import (
	"context"
	"testing"
	"time"
)

func TestBlockedPublishDoesNotBlockRemoteApplyAndPause(t *testing.T) {
	engine, _ := NewEngine("local")
	started := make(chan struct{})
	s, err := NewSynchronizer(engine, &memoryBackend{}, func(ctx context.Context, _ Entry) error { close(started); <-ctx.Done(); return ctx.Err() }, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	entry, _ := NewEntry("local", 1, "local", time.Now())
	done := make(chan error, 1)
	go func() { done <- s.publishEntry(ctx, entry) }()
	<-started
	remote, _ := NewEntry("remote", 1, "remote", time.Now())
	applied := make(chan error, 1)
	go func() { _, err := s.Apply(ctx, remote); applied <- err }()
	select {
	case err := <-applied:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("发送阻塞锁住远端更新")
	}
	paused := make(chan struct{})
	go func() { s.SetPaused(true); close(paused) }()
	select {
	case <-paused:
	case <-ctx.Done():
		t.Fatal("暂停未能取消阻塞发送")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
