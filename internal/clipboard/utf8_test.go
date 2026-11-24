package clipboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestEntryRejectsInvalidUTF8BeforeJSONCanChangeHash(t *testing.T) {
	if _, err := NewEntry("source", 1, string([]byte{'a', 0xff, 'b'}), time.Now()); err == nil {
		t.Fatal("接受了JSON序列化会改变的文本")
	}
	entry, err := NewEntry("source", 1, "中文\r\n🙂e\u0301", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(entry)
	var roundtrip Entry
	if err := json.Unmarshal(data, &roundtrip); err != nil || roundtrip.Validate() != nil {
		t.Fatal("合法Unicode文本不能往返")
	}
}

func TestUnsupportedLocalTextDoesNotStopInputConnection(t *testing.T) {
	engine, _ := NewEngine("source")
	backend := &memoryBackend{text: string([]byte{0xff}), revision: 1}
	published := make(chan Entry, 1)
	syncer, _ := NewSynchronizer(engine, backend, func(_ context.Context, entry Entry) error { published <- entry; return nil }, 20*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- syncer.Run(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("不支持的文本中断了连接: %v", err)
	case <-time.After(60 * time.Millisecond):
	}
	if err := backend.WriteText(ctx, "后续中文"); err != nil {
		t.Fatal(err)
	}
	select {
	case entry := <-published:
		if entry.Text != "后续中文" || entry.Validate() != nil {
			t.Fatal("后续正常文本同步失败")
		}
	case err := <-done:
		t.Fatalf("同步提前退出: %v", err)
	case <-ctx.Done():
		t.Fatal("未继续同步")
	}
	cancel()
	<-done
}
