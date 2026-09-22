package transport

import (
	"context"
	"errors"
	"io"

	"myapp/internal/protocol"
)

type Priority byte

const (
	PriorityRealtime Priority = iota + 1
	PriorityBulk
)

type PriorityWriter struct {
	realtime chan protocol.Frame
	bulk     chan protocol.Frame
	maxFrame uint32
}

func NewPriorityWriter(realtimeCapacity, bulkCapacity int, maxFrame uint32) (*PriorityWriter, error) {
	if realtimeCapacity < 1 || bulkCapacity < 1 {
		return nil, errors.New("发送队列容量必须大于零")
	}
	return &PriorityWriter{
		realtime: make(chan protocol.Frame, realtimeCapacity),
		bulk:     make(chan protocol.Frame, bulkCapacity),
		maxFrame: maxFrame,
	}, nil
}

func (w *PriorityWriter) Enqueue(ctx context.Context, priority Priority, frame protocol.Frame) error {
	frame.Payload = append([]byte(nil), frame.Payload...)
	var queue chan protocol.Frame
	switch priority {
	case PriorityRealtime:
		queue = w.realtime
	case PriorityBulk:
		queue = w.bulk
	default:
		return errors.New("消息优先级无效")
	}
	select {
	case queue <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *PriorityWriter) Run(ctx context.Context, writer io.Writer) error {
	for {
		if err := w.writeNext(ctx, writer); err != nil {
			return err
		}
	}
}

func (w *PriorityWriter) writeNext(ctx context.Context, writer io.Writer) error {
	select {
	case frame := <-w.realtime:
		return frame.Encode(writer, w.maxFrame)
	default:
	}
	select {
	case frame := <-w.realtime:
		return frame.Encode(writer, w.maxFrame)
	case frame := <-w.bulk:
		return frame.Encode(writer, w.maxFrame)
	case <-ctx.Done():
		return ctx.Err()
	}
}
