package fileclipboard

import (
	"context"
	"sync"
	"testing"
	"time"
)

type memoryBackend struct {
	mu       sync.Mutex
	paths    []string
	revision uint64
}

func (b *memoryBackend) ReadFiles(context.Context) ([]string, uint64, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.paths...), b.revision, len(b.paths) > 0, nil
}

func (b *memoryBackend) WriteFiles(_ context.Context, paths []string) error {
	b.mu.Lock()
	b.paths, b.revision = append([]string(nil), paths...), b.revision+1
	b.mu.Unlock()
	return nil
}

func TestPublishSuppressesReturnTransfer(t *testing.T) {
	backend := &memoryBackend{}
	sent := make(chan []string, 1)
	syncer, _ := NewSynchronizer(backend, func(_ context.Context, _ string, paths []string) error {
		sent <- paths
		return nil
	}, 50*time.Millisecond)
	if err := syncer.Publish(context.Background(), []string{"/cache/file"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Millisecond)
	defer cancel()
	_ = syncer.Run(ctx)
	select {
	case paths := <-sent:
		t.Fatalf("接收文件被回传: %v", paths)
	default:
	}
}
