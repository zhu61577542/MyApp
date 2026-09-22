package transport

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"time"

	"myapp/internal/protocol"
)

var (
	ErrHeartbeatPayload = errors.New("心跳载荷无效")
	ErrHeartbeatTimeout = errors.New("心跳超时")
)

type HeartbeatMonitor struct {
	mu       sync.RWMutex
	timeout  time.Duration
	lastSeen time.Time
}

func NewHeartbeatMonitor(timeout time.Duration, now time.Time) (*HeartbeatMonitor, error) {
	if timeout <= 0 {
		return nil, errors.New("心跳超时必须大于零")
	}
	return &HeartbeatMonitor{timeout: timeout, lastSeen: now}, nil
}

func (m *HeartbeatMonitor) Observe(now time.Time) {
	m.mu.Lock()
	if now.After(m.lastSeen) {
		m.lastSeen = now
	}
	m.mu.Unlock()
}

func (m *HeartbeatMonitor) Expired(now time.Time) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return now.Sub(m.lastSeen) > m.timeout
}

func NewHeartbeat(streamID uint64, now time.Time) protocol.Frame {
	payload := make([]byte, 8)
	binary.BigEndian.PutUint64(payload, uint64(now.UnixNano()))
	return protocol.Frame{Type: protocol.TypeHeartbeat, StreamID: streamID, Payload: payload}
}

func ParseHeartbeat(frame protocol.Frame) (time.Time, error) {
	if frame.Type != protocol.TypeHeartbeat || len(frame.Payload) != 8 {
		return time.Time{}, ErrHeartbeatPayload
	}
	return time.Unix(0, int64(binary.BigEndian.Uint64(frame.Payload))), nil
}

func RunHeartbeat(ctx context.Context, monitor *HeartbeatMonitor, interval time.Duration, send func(protocol.Frame) error) error {
	if monitor == nil || interval <= 0 || send == nil {
		return errors.New("心跳运行参数无效")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var sequence uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case now := <-ticker.C:
			if monitor.Expired(now) {
				return ErrHeartbeatTimeout
			}
			sequence++
			if err := send(NewHeartbeat(sequence, now)); err != nil {
				return err
			}
		}
	}
}
