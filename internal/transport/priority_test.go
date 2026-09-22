package transport

import (
	"bytes"
	"context"
	"testing"

	"myapp/internal/protocol"
)

func TestPriorityWriterSendsRealtimeFirst(t *testing.T) {
	writer, err := NewPriorityWriter(4, 4, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := writer.Enqueue(ctx, PriorityBulk, protocol.Frame{Type: protocol.TypeFileChunk, StreamID: 1, Payload: []byte("bulk")}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Enqueue(ctx, PriorityRealtime, protocol.Frame{Type: protocol.TypeInput, StreamID: 2, Payload: []byte("input")}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := writer.writeNext(ctx, &output); err != nil {
		t.Fatal(err)
	}
	if err := writer.writeNext(ctx, &output); err != nil {
		t.Fatal(err)
	}
	decoder := protocol.Decoder{MaxPayload: 1024}
	first, err := decoder.Decode(&output)
	if err != nil {
		t.Fatal(err)
	}
	second, err := decoder.Decode(&output)
	if err != nil {
		t.Fatal(err)
	}
	if first.Type != protocol.TypeInput || second.Type != protocol.TypeFileChunk {
		t.Fatalf("优先级错误: first=%d second=%d", first.Type, second.Type)
	}
}

func TestEnqueueCopiesPayload(t *testing.T) {
	writer, _ := NewPriorityWriter(1, 1, 1024)
	payload := []byte("safe")
	if err := writer.Enqueue(context.Background(), PriorityRealtime, protocol.Frame{Type: protocol.TypeInput, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X'
	var output bytes.Buffer
	if err := writer.writeNext(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	frame, err := (protocol.Decoder{}).Decode(&output)
	if err != nil || string(frame.Payload) != "safe" {
		t.Fatalf("载荷被外部修改: %q err=%v", frame.Payload, err)
	}
}
