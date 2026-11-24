package main

import (
	"context"
	"errors"
	common "myapp/internal/input"
	"time"
)

var errInputCongestion = errors.New("输入处理队列拥塞，已停止键鼠控制")

func captureQueued(parent context.Context, source common.Capturer, handler func(context.Context, common.Event) error, ready func()) error {
	return captureQueuedAfter(parent, source, handler, ready, nil, nil)
}
func captureQueuedAfter(parent context.Context, source common.Capturer, handler func(context.Context, common.Event) error, ready func(), start <-chan struct{}, filter func(common.Event) bool) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	events := make(chan common.Event, 256)
	ended := make(chan error, 1)
	go func() {
		if start != nil {
			select {
			case <-start:
			case <-ctx.Done():
				ended <- nil
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				ended <- nil
				return
			case event := <-events:
				if ctx.Err() != nil {
					ended <- nil
					return
				}
				delivery, done := context.WithTimeout(ctx, 100*time.Millisecond)
				err := handler(delivery, event)
				done()
				if err != nil {
					cancel()
					if err == context.Canceled {
						err = nil
					}
					ended <- err
					return
				}
			}
		}
	}()
	enqueue := func(event common.Event) error {
		if filter != nil && filter(event) {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case events <- event:
			return nil
		default:
			cancel()
			return errInputCongestion
		}
	}
	var result error
	if native, ok := source.(common.ReadyCapturer); ok {
		result = native.CaptureWithReady(ctx, enqueue, ready)
	} else if ready == nil {
		result = source.Capture(ctx, enqueue)
	} else {
		result = errors.New("当前捕获器不支持安全启动确认")
	}
	cancel()
	workerErr := <-ended
	if result == context.Canceled {
		result = nil
	}
	return errors.Join(result, workerErr)
}
