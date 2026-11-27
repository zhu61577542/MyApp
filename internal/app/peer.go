package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"myapp/internal/ownership"
	"myapp/internal/peercontrol"
	"myapp/internal/protocol"
	"myapp/internal/transport"
)

type PeerOptions struct {
	LocalID, RemoteID string
	Device            *peercontrol.Device
	Changed           func(ownership.Snapshot)
}

type peerOffer struct {
	Version int    `json:"version"`
	Nonce   string `json:"nonce"`
}
type peerOpen struct {
	Kind        string `json:"kind"`
	Nonce       string `json:"nonce"`
	RemoteNonce string `json:"remote_nonce"`
	Session     string `json:"session"`
	Paused      bool   `json:"paused"`
}

type linkPeer struct {
	mu                          sync.Mutex
	link                        *Link
	options                     PeerOptions
	ctx                         context.Context
	nonce, remoteNonce, session string
	mode                        string
	started                     time.Time
	ready                       chan struct{}
	readyOnce                   sync.Once
	node                        *peercontrol.Session
}

func (l *Link) EnablePeerControl(options PeerOptions) error {
	if options.Device == nil || options.LocalID == "" || options.RemoteID == "" || options.LocalID == options.RemoteID {
		return ownership.ErrInvalid
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	l.peer = &linkPeer{link: l, options: options, nonce: hex.EncodeToString(nonce[:]), mode: "pending", ready: make(chan struct{})}
	return nil
}

func (l *Link) PeerControl(ctx context.Context) (*peercontrol.Session, error) {
	if l.peer == nil {
		return nil, nil
	}
	select {
	case <-l.peer.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	l.peer.mu.Lock()
	defer l.peer.mu.Unlock()
	return l.peer.node, nil
}

func (l *Link) CurrentPeerControl() *peercontrol.Session {
	if l.peer == nil {
		return nil
	}
	l.peer.mu.Lock()
	defer l.peer.mu.Unlock()
	return l.peer.node
}

func (p *linkPeer) start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ctx, p.started = ctx, time.Now()
	payload, _ := json.Marshal(peerOffer{Version: 1, Nonce: p.nonce})
	writer := p.link.bulkWriter
	if writer == nil {
		writer = p.link.writer
	}
	return writer.Enqueue(ctx, transport.PriorityBulk, protocol.Frame{Type: protocol.TypePeerOffer, Payload: payload})
}

func (p *linkPeer) run(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		p.mu.Lock()
		if time.Since(p.started) > time.Second {
			if p.mode == "pending" {
				p.mode = "legacy"
				p.readyOnce.Do(func() { close(p.ready) })
			}
			if p.mode == "opening" {
				p.mu.Unlock()
				return errors.New("对等控制协商未完成")
			}
		}
		node := p.node
		p.mu.Unlock()
		if node != nil {
			if err := node.Tick(); err != nil {
				return err
			}
			if p.options.Changed != nil {
				p.options.Changed(node.Snapshot())
			}
			if err := node.Err(); err != nil {
				return err
			}
		}
	}
}

func (p *linkPeer) legacy() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == "opening" || p.mode == "peer" {
		return errors.New("对等控制会话拒绝旧版无令牌控制消息")
	}
	p.mode = "legacy"
	p.readyOnce.Do(func() { close(p.ready) })
	return nil
}

func (p *linkPeer) close() {
	p.mu.Lock()
	node := p.node
	p.mu.Unlock()
	if node != nil {
		node.Close()
	}
}

func (p *linkPeer) offer(ctx context.Context, frame protocol.Frame) error {
	var offer peerOffer
	if err := decodeStrictJSON(frame.Payload, &offer); err != nil {
		return err
	}
	if offer.Version != 1 {
		return nil
	}
	if !validNonce(offer.Nonce) {
		return ownership.ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == "legacy" {
		return nil
	}
	return p.openLocked(ctx, offer.Nonce)
}

func (p *linkPeer) openLocked(ctx context.Context, remoteNonce string) error {
	if p.remoteNonce != "" {
		if p.remoteNonce != remoteNonce {
			return ownership.ErrInvalid
		}
		return nil
	}
	p.remoteNonce = remoteNonce
	first, second := p.nonce, p.remoteNonce
	if p.options.LocalID > p.options.RemoteID {
		first, second = second, first
	}
	data, _ := json.Marshal([4]string{min(p.options.LocalID, p.options.RemoteID), max(p.options.LocalID, p.options.RemoteID), first, second})
	digest := sha256.Sum256(data)
	p.session = hex.EncodeToString(digest[:16])
	p.mode = "opening"
	payload, _ := json.Marshal(peerOpen{Kind: "open", Nonce: p.nonce, RemoteNonce: p.remoteNonce, Session: p.session, Paused: p.options.Device.Paused()})
	return p.link.writer.Enqueue(ctx, transport.PriorityRealtime, protocol.Frame{Type: protocol.TypePeerControl, Flags: protocol.FlagCritical, Payload: payload})
}

func (l *Link) handlePeerFrame(ctx context.Context, frame protocol.Frame) error {
	if frame.Type == protocol.TypePeerOffer {
		if l.peer == nil {
			return nil
		}
		if len(frame.Payload) > 1024 {
			return ownership.ErrInvalid
		}
		return l.peer.offer(ctx, frame)
	}
	if l.peer == nil || len(frame.Payload) > 8192 {
		return ownership.ErrInvalid
	}
	var header struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(frame.Payload, &header); err != nil {
		return err
	}
	p := l.peer
	if header.Kind == "open" {
		if frame.Type != protocol.TypePeerControl {
			return ownership.ErrInvalid
		}
		var open peerOpen
		if err := decodeStrictJSON(frame.Payload, &open); err != nil {
			return err
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.mode == "legacy" || !validNonce(open.Nonce) || open.RemoteNonce != p.nonce {
			return ownership.ErrInvalid
		}
		if err := p.openLocked(ctx, open.Nonce); err != nil {
			return err
		}
		if open.Session != p.session {
			return ownership.ErrInvalid
		}
		if p.node != nil {
			return nil
		}
		node, err := peercontrol.New(ctx, peercontrol.Config{Local: p.options.LocalID, Remote: p.options.RemoteID, Session: p.session, RemotePaused: open.Paused, Device: p.options.Device, Send: func(ctx context.Context, packet peercontrol.Packet) error {
			payload, err := json.Marshal(packet)
			if err != nil {
				return err
			}
			typeID := protocol.TypePeerControl
			if packet.Kind == "input" {
				typeID = protocol.TypePeerInput
			}
			return l.writer.Enqueue(ctx, transport.PriorityRealtime, protocol.Frame{Type: typeID, Flags: protocol.FlagCritical, Payload: payload})
		}})
		if err != nil {
			return err
		}
		p.node, p.mode = node, "peer"
		p.readyOnce.Do(func() { close(p.ready) })
		return nil
	}
	var packet peercontrol.Packet
	if err := decodeStrictJSON(frame.Payload, &packet); err != nil {
		return err
	}
	if (packet.Kind == "input") != (frame.Type == protocol.TypePeerInput) {
		return ownership.ErrInvalid
	}
	p.mu.Lock()
	node := p.node
	mode := p.mode
	p.mu.Unlock()
	if mode != "peer" || node == nil {
		return ownership.ErrInvalid
	}
	err := node.Handle(packet)
	if errors.Is(err, ownership.ErrStale) || errors.Is(err, ownership.ErrReplay) {
		return nil
	}
	return err
}

func validNonce(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 16 && hex.EncodeToString(data) == value
}
