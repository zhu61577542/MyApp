package transport

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Backoff struct {
	mu      sync.Mutex
	initial time.Duration
	maximum time.Duration
	current time.Duration
}

func NewBackoff(initial, maximum time.Duration) (*Backoff, error) {
	if initial <= 0 || maximum < initial {
		return nil, errors.New("重连退避参数无效")
	}
	return &Backoff{initial: initial, maximum: maximum}, nil
}

func (b *Backoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current == 0 {
		b.current = b.initial
		return b.current
	}
	if b.current >= b.maximum/2 {
		b.current = b.maximum
	} else {
		b.current *= 2
	}
	return b.current
}

func (b *Backoff) Reset() {
	b.mu.Lock()
	b.current = 0
	b.mu.Unlock()
}

func (b *Backoff) Wait(ctx context.Context) error {
	timer := time.NewTimer(b.Next())
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
