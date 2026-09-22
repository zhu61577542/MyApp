package clipboard

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestEngineSuppressesRemoteLoopAndStaleEntries(t *testing.T) {
	now := time.Unix(100, 0)
	first, _ := NewEngine("first")
	second, _ := NewEngine("second")
	entry, decision, err := first.Local("中文\ntext", now)
	if err != nil || decision != Accepted {
		t.Fatalf("Local() = %v, %v", decision, err)
	}
	if second.Remote(entry) != Accepted {
		t.Fatal("远端条目应被接受")
	}
	if _, decision, err = second.Local(entry.Text, now.Add(time.Second)); err != nil || decision != IgnoredDuplicate {
		t.Fatalf("回环未被抑制: %v, %v", decision, err)
	}
	if second.Remote(entry) != IgnoredStale {
		t.Fatal("旧版本应被拒绝")
	}
}

func TestApplyRemoteCanRetryAfterWriteFailure(t *testing.T) {
	engine, _ := NewEngine("target")
	entry, _ := NewEntry("source", 1, "hello", time.Now())
	if _, err := engine.ApplyRemote(entry, func(string) error { return errors.New("busy") }); err == nil {
		t.Fatal("首次写入应失败")
	}
	decision, err := engine.ApplyRemote(entry, func(string) error { return nil })
	if err != nil || decision != Accepted {
		t.Fatalf("重试结果 = %v, %v", decision, err)
	}
}

func TestEnginePause(t *testing.T) {
	engine, _ := NewEngine("device")
	engine.SetPaused(true)
	if _, decision, _ := engine.Local("text", time.Now()); decision != IgnoredPaused {
		t.Fatalf("暂停状态返回 %v", decision)
	}
}

type memoryBackend struct {
	mu       sync.Mutex
	text     string
	revision uint64
}

func (b *memoryBackend) ReadText(context.Context) (string, uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text, b.revision, nil
}

func (b *memoryBackend) WriteText(_ context.Context, text string) error {
	b.mu.Lock()
	b.text, b.revision = text, b.revision+1
	b.mu.Unlock()
	return nil
}

func TestSynchronizerApplyDoesNotRepublish(t *testing.T) {
	engine, _ := NewEngine("target")
	backend := &memoryBackend{}
	published := make(chan Entry, 1)
	syncer, err := NewSynchronizer(engine, backend, func(_ context.Context, entry Entry) error {
		published <- entry
		return nil
	}, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := NewEntry("source", 1, "hello", time.Now())
	if decision, err := syncer.Apply(context.Background(), entry); err != nil || decision != Accepted {
		t.Fatalf("Apply() = %v, %v", decision, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()
	_ = syncer.Run(ctx)
	select {
	case got := <-published:
		t.Fatalf("远端内容被重新发布: %+v", got)
	default:
	}
}
