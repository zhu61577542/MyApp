package peercontrol

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	common "myapp/internal/input"
	"myapp/internal/ownership"
	"myapp/internal/pointer"
)

const LeaseTime = 2 * time.Second

type Packet struct {
	Kind      string              `json:"kind"`
	Session   string              `json:"session"`
	Revision  uint64              `json:"revision,omitempty"`
	RequestID uint64              `json:"request_id,omitempty"`
	Sequence  uint64              `json:"sequence,omitempty"`
	Token     *ownership.Token    `json:"token,omitempty"`
	Fence     *ownership.Fence    `json:"fence,omitempty"`
	State     *ownership.Snapshot `json:"state,omitempty"`
	Entry     *pointer.Entry      `json:"entry,omitempty"`
	Event     *common.Event       `json:"event,omitempty"`
	Reason    ownership.Reason    `json:"reason,omitempty"`
	Error     string              `json:"error,omitempty"`
}

type Config struct {
	Local, Remote, Session string
	Device                 *Device
	RemotePaused           bool
	Send                   func(context.Context, Packet) error
	Now                    func() time.Time
}

type pendingRequest struct {
	id   uint64
	done chan error
	ctx  context.Context
}

type Session struct {
	mu         sync.Mutex
	config     Config
	ctx        context.Context
	cancel     context.CancelFunc
	machine    *ownership.Machine
	state      ownership.Snapshot
	revision   uint64
	request    uint64
	pending    *pendingRequest
	entries    map[string]*pointer.Entry
	preparing  ownership.Token
	blocked    ownership.Token
	restoring  ownership.Fence
	readyAt    time.Time
	validUntil time.Time
	renewals   map[uint64]time.Time
	renewSeq   uint64
	lastRenew  time.Time
	lastInput  uint64
	inputSeq   uint64
	inputToken ownership.Token
	granted    ownership.Token
	closed     bool
	err        error
}

func New(ctx context.Context, config Config) (*Session, error) {
	if config.Local == "" || config.Remote == "" || config.Local == config.Remote || config.Device == nil || config.Send == nil {
		return nil, ownership.ErrInvalid
	}
	paused := []string{}
	if config.Device.Paused() {
		paused = append(paused, config.Local)
	}
	if config.RemotePaused {
		paused = append(paused, config.Remote)
	}
	checked, err := ownership.New(ownership.Config{Devices: [2]string{config.Local, config.Remote}, SessionID: config.Session, InitiallyPaused: paused})
	if err != nil {
		return nil, err
	}
	if err := config.Device.bind(config.Session); err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{config: config, ctx: ctx, cancel: cancel, state: checked.Snapshot(), entries: make(map[string]*pointer.Entry), renewals: make(map[uint64]time.Time)}
	if config.Local < config.Remote {
		s.machine = checked
	}
	kind := "resume"
	if config.Device.Paused() {
		kind = "pause"
	}
	if err := s.actionLocked(config.Local, Packet{Kind: kind, Reason: ownership.Paused}); err != nil {
		s.closeLocked(err)
		return nil, err
	}
	return s, nil
}

func (s *Session) Snapshot() ownership.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyState(s.state)
}

func (s *Session) Err() error { s.mu.Lock(); defer s.mu.Unlock(); return s.err }

func (s *Session) Request(ctx context.Context, entry *pointer.Entry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed || s.pending != nil || s.state.Phase != ownership.Local && s.state.Phase != ownership.Preparing || s.config.Device.Paused() {
		s.mu.Unlock()
		return ownership.ErrBusy
	}
	if entry != nil && !entry.Valid() {
		s.mu.Unlock()
		return ownership.ErrInvalid
	}
	if s.request == math.MaxUint64 {
		s.mu.Unlock()
		return ownership.ErrExhausted
	}
	s.request++
	pending := &pendingRequest{id: s.request, done: make(chan error, 1), ctx: ctx}
	s.pending = pending
	err := s.actionLocked(s.config.Local, Packet{Kind: "request", RequestID: pending.id, Entry: entry})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case err := <-pending.done:
		return err
	case <-ctx.Done():
		s.mu.Lock()
		if s.pending == pending {
			s.finishRequestLocked(ctx.Err())
			s.config.Device.revoke(s.config.Session)
			s.localRestoreLocked()
		} else if s.state.Lease != nil && s.state.Lease.Source == s.config.Local && s.state.Lease.RequestID == pending.id {
			s.blocked = *s.state.Lease
			s.config.Device.revoke(s.config.Session)
			s.localRestoreLocked()
		}
		_ = s.actionLocked(s.config.Local, Packet{Kind: "cancel", RequestID: pending.id})
		s.mu.Unlock()
		return ctx.Err()
	case <-s.ctx.Done():
		return ownership.ErrClosed
	}
}

func (s *Session) SendInput(event common.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.config.Now()
	if s.closed || s.state.Phase != ownership.Active || s.state.Lease == nil || s.state.Lease.Source != s.config.Local || *s.state.Lease == s.blocked {
		return ownership.ErrStale
	}
	token := *s.state.Lease
	if token != s.inputToken {
		s.inputToken, s.inputSeq = token, 0
	}
	if s.inputSeq == math.MaxUint64 {
		s.stopLocked(ownership.LayoutChanged)
		return ownership.ErrExhausted
	}
	s.inputSeq++
	event.Sequence = s.inputSeq
	if err := event.Validate(); err != nil {
		return err
	}
	err := s.config.Device.input(token, now, func() error {
		if s.machine != nil {
			if err := s.machine.AcceptInput(s.config.Local, token, event.Sequence, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		err = s.sendLocked(Packet{Kind: "input", Token: &token, Event: &event})
	}
	if err != nil {
		s.stopLocked(ownership.Returned)
	}
	return err
}

func (s *Session) Stop(reason ownership.Reason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopLocked(reason)
}

func (s *Session) StopToken(token ownership.Token, reason ownership.Reason) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Lease == nil || *s.state.Lease != token {
		return ownership.ErrStale
	}
	return s.stopLocked(reason)
}

func (s *Session) stopLocked(reason ownership.Reason) error {
	if s.closed {
		return ownership.ErrClosed
	}
	s.config.Device.revoke(s.config.Session)
	if s.pending != nil {
		id := s.pending.id
		s.finishRequestLocked(ownership.ErrStale)
		if err := s.actionLocked(s.config.Local, Packet{Kind: "cancel", RequestID: id}); err != nil {
			return err
		}
	}
	token := s.state.Lease
	if token == nil {
		token = s.state.Previous
	}
	if token == nil {
		s.localRestoreLocked()
		return nil
	}
	s.blocked = *token
	s.localRestoreLocked()
	return s.actionLocked(s.config.Local, Packet{Kind: "release", Token: token, Reason: reason})
}

func (s *Session) Pause(paused bool, emergency bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ownership.ErrClosed
	}
	if err := s.config.Device.pauseSession(s.config.Session, paused); err != nil {
		return err
	}
	kind := "resume"
	if paused {
		kind = "pause"
		if s.state.Lease != nil {
			s.blocked = *s.state.Lease
		}
		s.finishRequestLocked(ownership.ErrPaused)
		s.localRestoreLocked()
	}
	reason := ownership.Paused
	if emergency {
		reason = ownership.Emergency
	}
	return s.actionLocked(s.config.Local, Packet{Kind: kind, Reason: reason})
}

func (s *Session) Handle(packet Packet) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || packet.Session != s.config.Session {
		return ownership.ErrStale
	}
	if packet.Kind == "input" {
		return s.inputLocked(packet)
	}
	if packet.Kind == "state" {
		if s.machine != nil || packet.State == nil || packet.Revision <= s.revision {
			return ownership.ErrStale
		}
		if err := s.validateState(*packet.State); err != nil {
			return err
		}
		if packet.Entry != nil && !packet.Entry.Valid() {
			return ownership.ErrInvalid
		}
		if packet.State.Lease != nil {
			var entry *pointer.Entry
			if packet.Entry != nil {
				copy := *packet.Entry
				entry = &copy
			}
			s.entries[packet.State.Lease.Source] = entry
		}
		s.revision, s.state = packet.Revision, copyState(*packet.State)
		return s.applyLocked()
	}
	if packet.Kind == "renewed" {
		return s.renewedLocked(packet)
	}
	if packet.Kind == "rejected" {
		if s.machine != nil {
			return ownership.ErrInvalid
		}
		if s.pending != nil && s.pending.id == packet.RequestID {
			s.finishRequestLocked(ownership.ErrBusy)
		}
		return nil
	}
	if s.machine == nil {
		return ownership.ErrInvalid
	}
	err := s.actionLocked(s.config.Remote, packet)
	if errors.Is(err, ownership.ErrBusy) || errors.Is(err, ownership.ErrStale) || errors.Is(err, ownership.ErrReplay) || errors.Is(err, ownership.ErrPaused) {
		return nil
	}
	return err
}

func (s *Session) Tick() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.err
	}
	now := s.config.Now()
	if s.machine != nil {
		state, err := s.machine.Tick(now)
		if err != nil {
			return err
		}
		if state.Fence != s.state.Fence || state.Phase != s.state.Phase {
			if err := s.publishLocked(); err != nil {
				return err
			}
		}
	}
	if s.state.Phase != ownership.Active || s.state.Lease == nil {
		return nil
	}
	if *s.state.Lease == s.blocked {
		return nil
	}
	if !now.Before(s.validUntil) {
		s.blocked = *s.state.Lease
		return s.stopLocked(ownership.Returned)
	}
	if now.Sub(s.lastRenew) < LeaseTime/4 {
		return nil
	}
	if s.renewSeq == math.MaxUint64 {
		return ownership.ErrExhausted
	}
	s.renewSeq++
	s.lastRenew = now
	s.renewals[s.renewSeq] = now
	for sequence, at := range s.renewals {
		if now.Sub(at) >= LeaseTime {
			delete(s.renewals, sequence)
		}
	}
	return s.actionLocked(s.config.Local, Packet{Kind: "renew", Token: s.state.Lease, Sequence: s.renewSeq})
}

func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked(nil)
}

func (s *Session) closeLocked(err error) {
	if s.closed {
		return
	}
	s.closed, s.err = true, err
	s.cancel()
	s.finishRequestLocked(ownership.ErrClosed)
	if s.machine != nil {
		s.state = s.machine.Disconnect(s.config.Now())
	} else {
		s.state.Connected = false
		s.state.Phase = ownership.Recovering
	}
	s.config.Device.restore(s.config.Session, true, func(err error) {
		if err != nil {
			s.mu.Lock()
			s.err = errors.Join(s.err, err)
			s.mu.Unlock()
		}
	})
}

func (s *Session) actionLocked(actor string, p Packet) error {
	if s.closed {
		return ownership.ErrClosed
	}
	if s.machine == nil {
		if actor != s.config.Local {
			return ownership.ErrUnknown
		}
		return s.sendLocked(p)
	}
	now := s.config.Now()
	var err error
	switch p.Kind {
	case "request":
		if p.Entry != nil && !p.Entry.Valid() {
			return ownership.ErrInvalid
		}
		_, err = s.machine.Request(actor, p.RequestID, now)
		if err == nil {
			if p.Entry != nil {
				entry := *p.Entry
				s.entries[actor] = &entry
			} else {
				s.entries[actor] = nil
			}
		}
	case "cancel":
		_, err = s.machine.CancelRequest(actor, p.RequestID, now)
	case "ready":
		if p.Token == nil {
			return ownership.ErrInvalid
		}
		_, err = s.machine.Ready(actor, *p.Token, now)
	case "restored":
		if p.Fence == nil {
			return ownership.ErrInvalid
		}
		_, err = s.machine.Restored(actor, *p.Fence, now)
	case "release":
		if p.Token == nil {
			return ownership.ErrInvalid
		}
		_, err = s.machine.Release(actor, *p.Token, p.Reason, now)
	case "pause":
		_, err = s.machine.Pause(actor, p.Reason == ownership.Emergency, now)
	case "resume":
		_, err = s.machine.Resume(actor, now)
	case "renew":
		if p.Token == nil {
			return ownership.ErrInvalid
		}
		_, err = s.machine.Renew(actor, *p.Token, p.Sequence, now)
	default:
		return ownership.ErrInvalid
	}
	// 错误也可能来自已触发的超时恢复。
	if publishErr := s.publishLocked(); publishErr != nil {
		return publishErr
	}
	if p.Kind == "request" && err != nil {
		if actor == s.config.Local {
			s.finishRequestLocked(err)
		} else if sendErr := s.sendLocked(Packet{Kind: "rejected", RequestID: p.RequestID}); sendErr != nil {
			return sendErr
		}
	}
	if p.Kind == "renew" && err == nil && actor == s.config.Remote {
		return s.sendLocked(Packet{Kind: "renewed", Token: p.Token, Sequence: p.Sequence, Revision: s.revision})
	}
	return err
}

func (s *Session) publishLocked() error {
	if s.revision == math.MaxUint64 {
		s.closeLocked(ownership.ErrExhausted)
		return ownership.ErrExhausted
	}
	s.state = s.machine.Snapshot()
	s.revision++
	wire := copyState(s.state)
	wire.Deadline, wire.WaitingDeadline = time.Time{}, time.Time{}
	var entry *pointer.Entry
	if wire.Lease != nil {
		entry = s.entries[wire.Lease.Source]
	}
	if err := s.sendLocked(Packet{Kind: "state", State: &wire, Revision: s.revision, Entry: entry}); err != nil {
		return err
	}
	return s.applyLocked()
}

func (s *Session) applyLocked() error {
	state := s.state
	if state.Phase == ownership.Preparing && state.Lease != nil {
		token := *state.Lease
		if token == s.preparing {
			return nil
		}
		if token.Source == s.config.Local && (s.pending == nil || s.pending.id != token.RequestID) {
			go s.cancelRequest(token.RequestID)
			return nil
		}
		s.preparing, s.readyAt, s.validUntil, s.lastInput = token, time.Time{}, time.Time{}, 0
		s.granted = ownership.Token{}
		err := s.config.Device.prepare(s.ctx, token, token.Source == s.config.Local, s.entries[token.Source], s.SendInput, func(emergency bool) { go s.nativeStopped(token, emergency) }, func(err error) { s.prepared(token, err) })
		if err != nil {
			s.err = err
			s.blocked = token
			go s.StopToken(token, ownership.PermissionLost)
		}
		return nil
	}
	if state.Phase == ownership.Active && state.Lease != nil {
		token := *state.Lease
		if token == s.blocked {
			return nil
		}
		if token.Source == s.config.Local && s.granted != token && (s.pending == nil || s.pending.ctx.Err() != nil) {
			s.blocked = token
			go s.StopToken(token, ownership.Returned)
			return nil
		}
		if token != s.preparing || s.readyAt.IsZero() {
			return ownership.ErrStale
		}
		if s.machine != nil {
			s.validUntil = state.Deadline
		} else if s.validUntil.IsZero() {
			s.validUntil = s.readyAt.Add(LeaseTime)
		}
		if err := s.config.Device.activate(s.ctx, token, s.validUntil, s.config.Now()); err != nil {
			s.err = err
			s.blocked = token
			go s.StopToken(token, ownership.Returned)
			return nil
		}
		if token.Source == s.config.Local {
			if s.pending != nil && s.pending.ctx.Err() != nil {
				s.blocked = token
				s.config.Device.revoke(s.config.Session)
				s.localRestoreLocked()
				go s.StopToken(token, ownership.Returned)
				return nil
			}
			s.granted = token
			s.finishRequestLocked(nil)
		}
		return nil
	}
	if state.Phase == ownership.Recovering {
		s.config.Device.revoke(s.config.Session)
		if s.pending != nil && (state.Waiting == nil || state.Waiting.Source != s.config.Local || state.Waiting.ID != s.pending.id) {
			s.finishRequestLocked(ownership.ErrStale)
		}
		if state.Fence == s.restoring {
			return nil
		}
		s.restoring = state.Fence
		fence := state.Fence
		s.config.Device.restore(s.config.Session, false, func(err error) { s.restored(fence, err) })
	}
	return nil
}

func (s *Session) prepared(token ownership.Token, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.state.Phase != ownership.Preparing || s.state.Lease == nil || *s.state.Lease != token {
		return
	}
	if err != nil {
		s.err = err
		s.blocked = token
		_ = s.actionLocked(s.config.Local, Packet{Kind: "release", Token: &token, Reason: ownership.PermissionLost})
		return
	}
	if token.Source == s.config.Local && (s.pending == nil || s.pending.id != token.RequestID || s.pending.ctx.Err() != nil) {
		s.config.Device.revoke(s.config.Session)
		s.localRestoreLocked()
		_ = s.actionLocked(s.config.Local, Packet{Kind: "cancel", RequestID: token.RequestID})
		return
	}
	if !s.readyAt.IsZero() {
		return
	}
	s.readyAt = s.config.Now()
	_ = s.actionLocked(s.config.Local, Packet{Kind: "ready", Token: &token})
}

func (s *Session) nativeStopped(token ownership.Token, emergency bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.state.Lease != nil && *s.state.Lease == token
	recovering := emergency && s.state.Phase == ownership.Recovering && s.state.Previous != nil && *s.state.Previous == token
	if s.closed || !current && !recovering {
		return
	}
	s.config.Device.revoke(s.config.Session)
	s.blocked = token
	s.localRestoreLocked()
	if emergency {
		_ = s.config.Device.pauseSession(s.config.Session, true)
		s.finishRequestLocked(ownership.ErrPaused)
		_ = s.actionLocked(s.config.Local, Packet{Kind: "pause", Reason: ownership.Emergency})
	} else {
		_ = s.stopLocked(ownership.Returned)
	}
}

func (s *Session) localRestoreLocked() {
	s.config.Device.restore(s.config.Session, false, func(err error) {
		if err != nil {
			s.mu.Lock()
			s.err = errors.Join(s.err, err)
			s.mu.Unlock()
		}
	})
}

func (s *Session) restored(fence ownership.Fence, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.err = err
		return
	}
	if s.closed || s.state.Phase != ownership.Recovering || s.state.Fence != fence {
		return
	}
	_ = s.actionLocked(s.config.Local, Packet{Kind: "restored", Fence: &fence})
}

func (s *Session) cancelRequest(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.actionLocked(s.config.Local, Packet{Kind: "cancel", RequestID: id})
}

func (s *Session) finishRequestLocked(err error) {
	if s.pending != nil {
		s.pending.done <- err
		s.pending = nil
	}
}

func (s *Session) sendLocked(p Packet) error {
	p.Session = s.config.Session
	ctx, cancel := context.WithTimeout(s.ctx, 100*time.Millisecond)
	defer cancel()
	if err := s.config.Send(ctx, p); err != nil {
		s.closeLocked(err)
		return err
	}
	return nil
}

func (s *Session) inputLocked(p Packet) error {
	if p.Token == nil || p.Event == nil || p.Token.Source != s.config.Remote || p.Token.Target != s.config.Local || s.state.Phase != ownership.Active || s.state.Lease == nil || *p.Token != *s.state.Lease || *p.Token == s.blocked {
		return ownership.ErrStale
	}
	if err := p.Event.Validate(); err != nil {
		return err
	}
	now := s.config.Now()
	err := s.config.Device.input(*p.Token, now, func() error {
		if s.machine != nil {
			if err := s.machine.AcceptInput(s.config.Remote, *p.Token, p.Event.Sequence, now); err != nil {
				return err
			}
		} else if p.Event.Sequence <= s.lastInput {
			return ownership.ErrReplay
		}
		s.lastInput = p.Event.Sequence
		return s.config.Device.driver.Inject(*p.Event)
	})
	if err != nil && !errors.Is(err, ownership.ErrReplay) {
		_ = s.stopLocked(ownership.Returned)
	}
	return err
}

func (s *Session) renewedLocked(p Packet) error {
	if s.machine != nil || p.Token == nil || s.state.Phase != ownership.Active || s.state.Lease == nil || *p.Token != *s.state.Lease || *p.Token == s.blocked || p.Revision != s.revision {
		return ownership.ErrStale
	}
	at, ok := s.renewals[p.Sequence]
	if !ok {
		return ownership.ErrReplay
	}
	now := s.config.Now()
	if !now.Before(s.validUntil) || !now.Before(at.Add(LeaseTime)) {
		s.blocked = *p.Token
		return s.stopLocked(ownership.Returned)
	}
	for seq := range s.renewals {
		if seq <= p.Sequence {
			delete(s.renewals, seq)
		}
	}
	s.validUntil = at.Add(LeaseTime)
	return s.config.Device.activate(s.ctx, *p.Token, s.validUntil, now)
}

func (s *Session) validateState(state ownership.Snapshot) error {
	if state.Session != s.config.Session || state.Coordinator != s.config.Remote || state.Epoch < s.state.Epoch || !state.Connected {
		return ownership.ErrInvalid
	}
	if state.Nodes[0].ID != s.config.Remote || state.Nodes[1].ID != s.config.Local {
		return ownership.ErrInvalid
	}
	if state.Phase != ownership.Local && state.Phase != ownership.Preparing && state.Phase != ownership.Active && state.Phase != ownership.Recovering {
		return ownership.ErrInvalid
	}
	if state.Phase == ownership.Preparing || state.Phase == ownership.Active {
		if state.Lease == nil || state.Lease.Fence != state.Fence || state.Lease.RequestID == 0 || state.Lease.Source == state.Lease.Target {
			return ownership.ErrInvalid
		}
		if state.Lease.Source != s.config.Local && state.Lease.Source != s.config.Remote || state.Lease.Target != s.config.Local && state.Lease.Target != s.config.Remote {
			return ownership.ErrInvalid
		}
	}
	return nil
}

func copyState(state ownership.Snapshot) ownership.Snapshot {
	if state.Lease != nil {
		v := *state.Lease
		state.Lease = &v
	}
	if state.Previous != nil {
		v := *state.Previous
		state.Previous = &v
	}
	if state.Waiting != nil {
		v := *state.Waiting
		state.Waiting = &v
	}
	return state
}
