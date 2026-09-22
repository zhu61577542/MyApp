package fileclipboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type Backend interface {
	ReadFiles(context.Context) (paths []string, revision uint64, available bool, err error)
	WriteFiles(context.Context, []string) error
}

type Sender func(context.Context, string, []string) error

type Synchronizer struct {
	backend  Backend
	send     Sender
	period   time.Duration
	mu       sync.Mutex
	lastSeen uint64
	suppress uint64
}

func NewSynchronizer(backend Backend, send Sender, period time.Duration) (*Synchronizer, error) {
	if backend == nil || send == nil || period < 50*time.Millisecond {
		return nil, errors.New("文件剪贴板同步参数无效")
	}
	return &Synchronizer{backend: backend, send: send, period: period}, nil
}

func (s *Synchronizer) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.period)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			paths, revision, available, err := s.backend.ReadFiles(ctx)
			if err != nil {
				return err
			}
			s.mu.Lock()
			if revision == s.lastSeen {
				s.mu.Unlock()
				continue
			}
			s.lastSeen = revision
			suppressed := revision == s.suppress
			s.mu.Unlock()
			if !available || suppressed || len(paths) == 0 {
				continue
			}
			transferID, err := randomTransferID()
			if err != nil {
				return err
			}
			if err := s.send(ctx, transferID, paths); err != nil {
				return err
			}
		}
	}
}

func (s *Synchronizer) Publish(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return errors.New("发布文件路径不能为空")
	}
	if err := s.backend.WriteFiles(ctx, paths); err != nil {
		return err
	}
	_, revision, available, err := s.backend.ReadFiles(ctx)
	if err != nil {
		return err
	}
	if !available {
		return errors.New("平台未保留已发布的文件剪贴板")
	}
	s.mu.Lock()
	s.lastSeen, s.suppress = revision, revision
	s.mu.Unlock()
	return nil
}

func randomTransferID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
