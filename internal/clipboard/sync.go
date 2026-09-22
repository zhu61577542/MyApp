package clipboard

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Backend interface {
	ReadText(context.Context) (text string, revision uint64, err error)
	WriteText(context.Context, string) error
}

type Publisher func(context.Context, Entry) error

type Synchronizer struct {
	engine  *Engine
	backend Backend
	publish Publisher
	period  time.Duration
	writeMu sync.Mutex
}

func NewSynchronizer(engine *Engine, backend Backend, publish Publisher, period time.Duration) (*Synchronizer, error) {
	if engine == nil || backend == nil || publish == nil {
		return nil, errors.New("剪贴板同步依赖不能为空")
	}
	if period < 20*time.Millisecond {
		return nil, errors.New("剪贴板轮询周期不能小于 20ms")
	}
	return &Synchronizer{engine: engine, backend: backend, publish: publish, period: period}, nil
}

func (s *Synchronizer) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.period)
	defer ticker.Stop()
	var revision uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if s.engine.Paused() {
				continue
			}
			text, current, err := s.backend.ReadText(ctx)
			if err != nil {
				return err
			}
			if current == revision {
				continue
			}
			revision = current
			entry, decision, err := s.engine.Local(text, time.Now())
			if err != nil {
				return err
			}
			if decision == Accepted {
				if err := s.publish(ctx, entry); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Synchronizer) Apply(ctx context.Context, entry Entry) (Decision, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.engine.ApplyRemote(entry, func(text string) error {
		return s.backend.WriteText(ctx, text)
	})
}
