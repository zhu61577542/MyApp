package app

import (
	"bytes"
	"context"
	"testing"
	"time"

	common "myapp/internal/input"
	"myapp/internal/protocol"
	"myapp/internal/transport"
)

func TestRequirementReplayedInputIsNotInjectedTwice(t *testing.T) {
	injector := &recordingInjector{}
	link, _ := NewLink(injector, nil)
	var payload, wire bytes.Buffer
	if err := (common.Event{Kind: common.KeyDown, Sequence: 1, Code: 4}).Encode(&payload); err != nil {
		t.Fatal(err)
	}
	frame := protocol.Frame{Type: protocol.TypeInput, Flags: protocol.FlagCritical, StreamID: 1, Payload: payload.Bytes()}
	for range 2 {
		if err := frame.Encode(&wire, 0); err != nil {
			t.Fatal(err)
		}
	}
	monitor, _ := transport.NewHeartbeatMonitor(time.Second, time.Now())
	_ = link.read(context.Background(), &wire, monitor)
	if len(injector.events) != 1 {
		t.Fatalf("同一输入事件被注入 %d 次", len(injector.events))
	}
}
