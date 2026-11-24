package main

import (
	"context"
	"errors"
	common "myapp/internal/input"
	"testing"
	"time"
)

type queuedTestCapturer struct {
	produce func(context.Context, func(common.Event) error) error
}

func (c *queuedTestCapturer) Capture(ctx context.Context, handler func(common.Event) error) error {
	return c.produce(ctx, handler)
}
func (c *queuedTestCapturer) CaptureWithReady(ctx context.Context, handler func(common.Event) error, ready func()) error {
	if ready != nil {
		ready()
	}
	return c.produce(ctx, handler)
}
func (*queuedTestCapturer) Close() error { return nil }

func TestCaptureQueueOverflowCancelsAndJoinsWorker(t *testing.T) {
	ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
	defer done()
	entered, exited := make(chan struct{}), make(chan struct{})
	source := &queuedTestCapturer{produce: func(ctx context.Context, send func(common.Event) error) error {
		if err := send(common.Event{Kind: common.KeyDown, Code: 4, Sequence: 1}); err != nil {
			return err
		}
		select {
		case <-entered:
		case <-ctx.Done():
			return ctx.Err()
		}
		for sequence := 2; sequence < 1024; sequence++ {
			if err := send(common.Event{Kind: common.KeyUp, Code: 4, Sequence: uint64(sequence)}); err != nil {
				return err
			}
		}
		return errors.New("未触发输入拥塞")
	}}
	err := captureQueued(ctx, source, func(ctx context.Context, _ common.Event) error {
		close(entered)
		defer close(exited)
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	if !errors.Is(err, errInputCongestion) {
		t.Fatalf("拥塞未明确失败: %v", err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("返回时后台发送仍未退出")
	}
}

func TestCaptureQueuePreservesWorkerFailure(t *testing.T) {
	ctx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	want := errors.New("sender-test-error")
	source := &queuedTestCapturer{produce: func(ctx context.Context, send func(common.Event) error) error {
		if err := send(common.Event{Kind: common.KeyDown, Code: 4, Sequence: 1}); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	if err := captureQueued(ctx, source, func(context.Context, common.Event) error { return want }, nil); !errors.Is(err, want) {
		t.Fatalf("发送错误被取消状态覆盖: %v", err)
	}
}

func TestHandoffBuffersMotionAndKeysUntilTargetConfirmed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	gate, buffered := make(chan struct{}), make(chan struct{})
	delivered := make(chan common.Event, 3)
	source := &queuedTestCapturer{produce: func(ctx context.Context, send func(common.Event) error) error {
		for n, kind := range []common.Kind{common.MouseRelative, common.KeyDown, common.KeyUp} {
			event := common.Event{Kind: kind, Sequence: uint64(n + 1), Code: 4, X: 37}
			if kind == common.MouseRelative {
				event.Code = 0
			} else {
				event.X = 0
			}
			if err := send(event); err != nil {
				return err
			}
		}
		close(buffered)
		<-ctx.Done()
		return ctx.Err()
	}}
	ended := make(chan error, 1)
	go func() {
		ended <- captureQueuedAfter(ctx, source, func(_ context.Context, event common.Event) error {
			delivered <- event
			if event.Sequence == 3 {
				cancel()
			}
			return nil
		}, nil, gate, nil)
	}()
	select {
	case <-buffered:
	case <-ctx.Done():
		t.Fatal("等待确认时未捕获输入")
	}
	if len(delivered) != 0 {
		t.Fatal("目标确认前转发了输入")
	}
	close(gate)
	if err := <-ended; err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= 3; seq++ {
		event := <-delivered
		if event.Sequence != uint64(seq) {
			t.Fatal("切换期间丢失或重排输入")
		}
	}
}

func TestEmergencyCancelsHandoffBeforeConfirmation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	source := &queuedTestCapturer{produce: func(ctx context.Context, send func(common.Event) error) error {
		send(common.Event{Kind: common.KeyDown, Code: 41, Sequence: 1})
		<-ctx.Done()
		return ctx.Err()
	}}
	if err := captureQueuedAfter(ctx, source, func(context.Context, common.Event) error { t.Error("确认前转发了输入"); return nil }, nil, make(chan struct{}), func(common.Event) bool { cancel(); return true }); err != nil {
		t.Fatal(err)
	}
}
