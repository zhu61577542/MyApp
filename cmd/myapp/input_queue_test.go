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
