package app

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/peercontrol"
	"myapp/internal/pointer"
	"myapp/internal/protocol"
)

type positionTestDriver struct{}

func (*positionTestDriver) Prepare(context.Context, ownership.Token, bool, *pointer.Entry, func(common.Event) error, func(bool)) error {
	return nil
}
func (*positionTestDriver) Activate(context.Context, ownership.Token) error { return nil }
func (*positionTestDriver) Restore(context.Context) error                   { return nil }
func (*positionTestDriver) Inject(common.Event) error                       { return nil }

func positionPair(t *testing.T) ([2]*Link, *atomic.Value, [2]*atomic.Bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	var links [2]*Link
	ready := [2]*atomic.Bool{new(atomic.Bool), new(atomic.Bool)}
	side := new(atomic.Value)
	side.Store("left")
	ids := [2]string{"a", "b"}
	for i := range links {
		links[i], _ = NewSplitLink(nil, nil)
		if err := links[i].EnablePeerControl(PeerOptions{LocalID: ids[i], RemoteID: ids[1-i], Device: peercontrol.NewDevice(&positionTestDriver{})}); err != nil {
			t.Fatal(err)
		}
		links[i].EnablePeerPosition(i == 0, func() string { return side.Load().(string) }, ready[i].Load)
	}
	a, b := net.Pipe()
	x, y := net.Pipe()
	done := make(chan error, 2)
	go func() { done <- links[0].RunChannels(ctx, a, x) }()
	go func() { done <- links[1].RunChannels(ctx, b, y) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("位置会话没有退出")
			}
		}
	})
	return links, side, ready
}

func waitPosition(t *testing.T, check func() bool) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		if check() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("位置协商未达到预期状态")
}

func TestPeerPositionWaitsForBothPlatformsAndUsesOppositeSides(t *testing.T) {
	links, side, ready := positionPair(t)
	ready[0].Store(true)
	waitPosition(t, func() bool { return links[0].CurrentPeerControl() != nil && links[1].CurrentPeerControl() != nil })
	if _, ok := links[1].PeerPosition(); ok {
		t.Fatal("本机监听未就绪就开放反向入口")
	}
	ready[1].Store(true)
	waitPosition(t, func() bool {
		a, ok := links[0].PeerPosition()
		b, yes := links[1].PeerPosition()
		return ok && yes && a == "left" && b == "right"
	})
	links[0].position.mu.Lock()
	old := links[0].position.current
	links[0].position.mu.Unlock()
	side.Store("right")
	if _, ok := links[0].PeerPosition(); ok {
		t.Fatal("布局已变仍使用旧确认方向")
	}
	waitPosition(t, func() bool {
		a, ok := links[0].PeerPosition()
		b, yes := links[1].PeerPosition()
		return ok && yes && a == "right" && b == "left"
	})
	old.Ack, old.Ready = true, true
	payload, _ := json.Marshal(old)
	if err := links[0].handlePeerPosition(protocol.Frame{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if side, ok := links[0].PeerPosition(); !ok || side != "right" {
		t.Fatal("旧修订覆盖了新布局")
	}
	old.Session = "old-session"
	payload, _ = json.Marshal(old)
	if err := links[1].handlePeerPosition(protocol.Frame{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if side, ok := links[1].PeerPosition(); !ok || side != "left" {
		t.Fatal("旧会话布局生效")
	}
	ready[1].Store(false)
	waitPosition(t, func() bool { _, ok := links[0].PeerPosition(); return !ok })
}

func TestPeerPositionRejectsMalformedPlacement(t *testing.T) {
	link, _ := NewLink(nil, nil)
	link.EnablePeerPosition(false, func() string { return "" }, func() bool { return true })
	for _, data := range []string{`{"version":1,"side":"inside","revision":1}`, `{"version":1,"side":"left","revision":0}`, `{"version":1,"side":"left","revision":1,"extra":true}`} {
		if err := link.handlePeerPosition(protocol.Frame{Payload: []byte(data)}); err == nil {
			t.Fatalf("接受无效布局 %s", data)
		}
	}
}
