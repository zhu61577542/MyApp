package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"myapp/internal/pointer"
	"myapp/internal/protocol"
	"myapp/internal/transport"
)

type peerPositionMessage struct {
	Version  int    `json:"version"`
	Session  string `json:"session"`
	Side     string `json:"side"`
	Revision uint64 `json:"revision"`
	Ack      bool   `json:"ack"`
	Ready    bool   `json:"ready"`
}

type peerPosition struct {
	mu          sync.Mutex
	authority   bool
	local       func() string
	ready       func() bool
	current     peerPositionMessage
	confirmed   bool
	remoteReady bool
	known       bool
}

func (l *Link) PeerPositionKnown() bool {
	if l.position == nil {
		return false
	}
	l.position.mu.Lock()
	defer l.position.mu.Unlock()
	return l.position.known
}

func (l *Link) EnablePeerPosition(authority bool, local func() string, ready func() bool) {
	l.position = &peerPosition{authority: authority, local: local, ready: ready}
}

func (l *Link) PeerPosition() (string, bool) {
	p := l.position
	node := l.CurrentPeerControl()
	if p == nil || node == nil || !p.ready() {
		return "", false
	}
	session := node.Snapshot().Session
	localSide := ""
	if p.authority {
		localSide = p.local()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current.Session != session || !p.remoteReady || !p.confirmed {
		return "", false
	}
	if p.authority {
		if p.current.Side != localSide {
			return "", false
		}
		return p.current.Side, true
	}
	return pointer.OppositeSide(p.current.Side), true
}

func (l *Link) publishPeerPosition(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	var lastSent time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		node := l.CurrentPeerControl()
		if node == nil {
			continue
		}
		session := node.Snapshot().Session
		p := l.position
		side := ""
		if p.authority {
			side = p.local()
		}
		ready := p.ready()
		p.mu.Lock()
		if p.authority {
			if pointer.OppositeSide(side) == "" {
				p.mu.Unlock()
				continue
			}
			if p.current.Session != session || p.current.Side != side {
				p.current = peerPositionMessage{Version: 1, Session: session, Side: side, Revision: p.current.Revision + 1}
				p.confirmed, p.remoteReady = false, false
			}
		} else if p.current.Session != session {
			p.mu.Unlock()
			continue
		}
		message := p.current
		message.Ack, message.Ready = !p.authority, ready
		p.mu.Unlock()
		payload, _ := json.Marshal(message)
		if string(payload) == last && time.Since(lastSent) < time.Second {
			continue
		}
		writer := l.bulkWriter
		if writer == nil {
			writer = l.writer
		}
		if err := writer.Enqueue(ctx, transport.PriorityBulk, protocol.Frame{Type: protocol.TypePeerPosition, Payload: payload}); err != nil {
			return
		}
		last = string(payload)
		lastSent = time.Now()
	}
}

func (l *Link) handlePeerPosition(frame protocol.Frame) error {
	if l.position == nil {
		return nil
	}
	if len(frame.Payload) > 512 {
		return errors.New("对等位置消息过长")
	}
	var message peerPositionMessage
	if err := decodeStrictJSON(frame.Payload, &message); err != nil {
		return err
	}
	if message.Version != 1 || message.Revision == 0 || pointer.OppositeSide(message.Side) == "" {
		return errors.New("对等位置无效")
	}
	node := l.CurrentPeerControl()
	if node == nil || message.Session != node.Snapshot().Session {
		return nil
	}
	p := l.position
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.authority {
		if message.Ack && message.Revision == p.current.Revision && message.Side == p.current.Side && message.Session == p.current.Session {
			p.known = true
			p.confirmed, p.remoteReady = true, message.Ready
		}
	} else if !message.Ack && (p.current.Session != message.Session || message.Revision >= p.current.Revision) {
		if message.Revision == p.current.Revision && p.current.Session == message.Session && message.Side != p.current.Side {
			return errors.New("同一位置修订包含冲突布局")
		}
		p.current, p.confirmed, p.remoteReady, p.known = message, true, message.Ready, true
	}
	return nil
}
