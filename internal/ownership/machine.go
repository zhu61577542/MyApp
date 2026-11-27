package ownership

import (
	"crypto/rand"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type Machine struct {
	mu           sync.Mutex
	devices      [2]string
	session      string
	prepareTime  time.Duration
	leaseTime    time.Duration
	phase        Phase
	epoch        uint64
	connected    bool
	paused       [2]bool
	lease        Token
	previous     Token
	waiting      *Request
	waitingUntil time.Time
	ready        [2]bool
	restored     [2]bool
	lastRequest  [2]uint64
	lastRenew    [2]uint64
	heartbeat    [2]time.Time
	lastInput    uint64
	deadline     time.Time
	lastNow      time.Time
	reason       Reason
}

func New(config Config) (*Machine, error) {
	for _, id := range config.Devices {
		if id == "" || len(id) > 128 {
			return nil, ErrInvalid
		}
		for _, ch := range id {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
				return nil, ErrInvalid
			}
		}
	}
	if config.Devices[0] == config.Devices[1] {
		return nil, ErrInvalid
	}
	if config.SessionID == "" {
		value := make([]byte, 16)
		if _, err := rand.Read(value); err != nil {
			return nil, err
		}
		config.SessionID = hex.EncodeToString(value)
	}
	id, err := hex.DecodeString(config.SessionID)
	if err != nil || len(id) != 16 || config.SessionID != strings.ToLower(config.SessionID) || config.SessionID == strings.Repeat("0", 32) {
		return nil, ErrInvalid
	}
	if config.PrepareTimeout == 0 {
		config.PrepareTimeout = 2 * time.Second
	}
	if config.LeaseTimeout == 0 {
		config.LeaseTimeout = 2 * time.Second
	}
	if config.PrepareTimeout < time.Millisecond || config.PrepareTimeout > time.Minute || config.LeaseTimeout < time.Millisecond || config.LeaseTimeout > time.Minute {
		return nil, ErrInvalid
	}
	sort.Strings(config.Devices[:])
	m := &Machine{devices: config.Devices, session: config.SessionID, prepareTime: config.PrepareTimeout, leaseTime: config.LeaseTimeout, phase: Local, connected: true}
	for _, actor := range config.InitiallyPaused {
		index := m.index(actor)
		if index < 0 {
			return nil, ErrInvalid
		}
		m.paused[index] = true
	}
	return m, nil
}

func (m *Machine) Request(actor string, id uint64, now time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.index(actor)
	if index < 0 {
		return m.snapshot(), ErrUnknown
	}
	if id == 0 {
		return m.snapshot(), ErrInvalid
	}
	fresh := id > m.lastRequest[index]
	if fresh {
		m.lastRequest[index] = id
	}
	if err := m.advance(now); err != nil {
		return m.snapshot(), err
	}
	if !m.connected {
		return m.snapshot(), ErrClosed
	}
	if m.paused[0] || m.paused[1] {
		return m.snapshot(), ErrPaused
	}
	if !fresh {
		if m.lease.Source == actor && m.lease.RequestID == id && (m.phase == Preparing || m.phase == Active) || m.waiting != nil && m.waiting.Source == actor && m.waiting.ID == id {
			return m.snapshot(), nil
		}
		return m.snapshot(), ErrStale
	}
	if m.phase == Recovering || m.phase == Active {
		return m.snapshot(), ErrBusy
	}
	if m.phase == Preparing {
		if actor == m.lease.Source || actor > m.lease.Source {
			return m.snapshot(), ErrBusy
		}
		m.recover(Collision)
		if m.connected {
			m.waiting = &Request{Source: actor, ID: id}
			m.waitingUntil = now.Add(m.prepareTime)
		} else {
			return m.snapshot(), ErrExhausted
		}
		return m.snapshot(), nil
	}
	err := m.prepare(Request{Source: actor, ID: id}, now)
	return m.snapshot(), err
}

func (m *Machine) Ready(actor string, token Token, now time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.index(actor)
	if index < 0 {
		return m.snapshot(), ErrUnknown
	}
	if err := m.advance(now); err != nil {
		return m.snapshot(), err
	}
	if token != m.lease || (m.phase != Preparing && m.phase != Active) {
		return m.snapshot(), ErrStale
	}
	if m.phase == Active {
		return m.snapshot(), nil
	}
	if !m.ready[index] {
		m.ready[index], m.heartbeat[index] = true, now
	}
	if m.ready[0] && m.ready[1] {
		m.phase = Active
		m.lastRenew = [2]uint64{}
		m.lastInput = 0
		m.updateDeadline()
		if !now.Before(m.deadline) {
			m.recover(TimedOut)
			return m.snapshot(), ErrStale
		}
	}
	return m.snapshot(), nil
}

func (m *Machine) CancelRequest(actor string, id uint64, now time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.index(actor)
	if index < 0 {
		return m.snapshot(), ErrUnknown
	}
	if id == 0 {
		return m.snapshot(), ErrInvalid
	}
	if id > m.lastRequest[index] {
		m.lastRequest[index] = id
	}
	m.stopTime(now)
	if (m.phase == Preparing || m.phase == Active) && m.lease.Source == actor && m.lease.RequestID == id {
		m.recover(Cancelled)
	} else if m.waiting != nil && m.waiting.Source == actor && m.waiting.ID == id {
		m.waiting, m.waitingUntil = nil, time.Time{}
		m.reason = Cancelled
	}
	return m.snapshot(), nil
}

func (m *Machine) Renew(actor string, token Token, sequence uint64, now time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.index(actor)
	if index < 0 {
		return m.snapshot(), ErrUnknown
	}
	if err := m.advance(now); err != nil {
		return m.snapshot(), err
	}
	if m.phase != Active || token != m.lease {
		return m.snapshot(), ErrStale
	}
	if sequence == 0 || sequence <= m.lastRenew[index] {
		return m.snapshot(), ErrReplay
	}
	m.lastRenew[index], m.heartbeat[index] = sequence, now
	m.updateDeadline()
	return m.snapshot(), nil
}

func (m *Machine) updateDeadline() {
	earlier := m.heartbeat[0]
	if m.heartbeat[1].Before(earlier) {
		earlier = m.heartbeat[1]
	}
	m.deadline = earlier.Add(m.leaseTime)
}

func (m *Machine) AcceptInput(actor string, token Token, sequence uint64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.index(actor) < 0 {
		return ErrUnknown
	}
	if err := m.advance(now); err != nil {
		return err
	}
	if m.phase != Active || token != m.lease {
		return ErrStale
	}
	if actor != m.lease.Source {
		return ErrNotSource
	}
	if sequence == 0 || sequence <= m.lastInput {
		return ErrReplay
	}
	m.lastInput = sequence
	return nil
}

func (m *Machine) Release(actor string, token Token, reason Reason, now time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.index(actor) < 0 {
		return m.snapshot(), ErrUnknown
	}
	if reason != Returned && reason != LocalTakeover && reason != PermissionLost && reason != LayoutChanged {
		return m.snapshot(), ErrInvalid
	}
	if token == m.previous && token.Epoch != 0 {
		if m.phase == Recovering && reason != Returned {
			m.stopTime(now)
			m.waiting, m.waitingUntil = nil, time.Time{}
			m.reason = reason
		}
		return m.snapshot(), nil
	}
	if token != m.lease || m.phase != Preparing && m.phase != Active {
		return m.snapshot(), ErrStale
	}
	m.stopTime(now)
	m.recover(reason)
	return m.snapshot(), nil
}

func (m *Machine) Pause(actor string, emergency bool, now time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.index(actor)
	if index < 0 {
		return m.snapshot(), ErrUnknown
	}
	m.stopTime(now)
	m.paused[index] = true
	m.waiting = nil
	m.waitingUntil = time.Time{}
	reason := Paused
	if emergency {
		reason = Emergency
	}
	if m.phase == Active || m.phase == Preparing {
		m.recover(reason)
	} else {
		m.reason = reason
	}
	return m.snapshot(), nil
}

func (m *Machine) Resume(actor string, now time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.index(actor)
	if index < 0 {
		return m.snapshot(), ErrUnknown
	}
	if err := m.advance(now); err != nil {
		return m.snapshot(), err
	}
	if !m.connected {
		return m.snapshot(), ErrClosed
	}
	m.paused[index] = false
	return m.snapshot(), nil
}

func (m *Machine) Disconnect(now time.Time) Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopTime(now)
	if m.connected {
		m.connected = false
		m.waiting = nil
		m.waitingUntil = time.Time{}
		if m.phase == Active || m.phase == Preparing {
			m.recover(Disconnected)
		} else {
			m.reason = Disconnected
		}
	}
	return m.snapshot()
}

func (m *Machine) Restored(actor string, fence Fence, now time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.index(actor)
	if index < 0 {
		return m.snapshot(), ErrUnknown
	}
	if err := m.advance(now); err != nil {
		return m.snapshot(), err
	}
	if fence != m.fence() || m.phase != Recovering && !(m.phase == Local && m.restored[index]) {
		return m.snapshot(), ErrStale
	}
	m.restored[index] = true
	if m.restored[0] && m.restored[1] && m.phase == Recovering {
		m.phase = Local
		waiting := m.waiting
		m.waiting = nil
		m.waitingUntil = time.Time{}
		if waiting != nil && m.connected && !m.paused[0] && !m.paused[1] {
			if err := m.prepare(*waiting, now); err != nil {
				return m.snapshot(), err
			}
		}
	}
	return m.snapshot(), nil
}

func (m *Machine) Tick(now time.Time) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.advance(now)
	return m.snapshot(), err
}

func (m *Machine) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshot()
}

func (m *Machine) index(actor string) int {
	for index, id := range m.devices {
		if id == actor {
			return index
		}
	}
	return -1
}

func (m *Machine) fence() Fence { return Fence{Session: m.session, Epoch: m.epoch} }

func (m *Machine) bump() bool {
	if m.epoch == math.MaxUint64 {
		m.connected, m.phase, m.reason = false, Recovering, Exhausted
		m.lease, m.waiting, m.deadline = Token{}, nil, time.Time{}
		m.waitingUntil = time.Time{}
		m.restored = [2]bool{}
		return false
	}
	m.epoch++
	return true
}

func (m *Machine) prepare(request Request, now time.Time) error {
	if !m.bump() {
		return ErrExhausted
	}
	m.lease = Token{Fence: m.fence(), Source: request.Source, Target: m.devices[1-m.index(request.Source)], RequestID: request.ID}
	m.phase, m.reason = Preparing, ""
	m.ready, m.restored = [2]bool{}, [2]bool{}
	m.deadline = now.Add(m.prepareTime)
	return nil
}

func (m *Machine) recover(reason Reason) {
	m.previous = m.lease
	m.lease, m.waiting, m.deadline = Token{}, nil, time.Time{}
	m.waitingUntil = time.Time{}
	m.phase, m.reason = Recovering, reason
	m.ready, m.restored = [2]bool{}, [2]bool{}
	m.bump()
}

func (m *Machine) advance(now time.Time) error {
	if now.IsZero() || !m.lastNow.IsZero() && now.Before(m.lastNow) {
		return ErrClock
	}
	m.lastNow = now
	if m.waiting != nil && !now.Before(m.waitingUntil) {
		m.waiting, m.waitingUntil = nil, time.Time{}
		m.reason = TimedOut
	}
	if (m.phase == Preparing || m.phase == Active) && !now.Before(m.deadline) {
		m.recover(TimedOut)
	}
	return nil
}

func (m *Machine) stopTime(now time.Time) {
	if !now.IsZero() && (m.lastNow.IsZero() || !now.Before(m.lastNow)) {
		m.lastNow = now
	}
}

func (m *Machine) snapshot() Snapshot {
	s := Snapshot{Fence: m.fence(), Coordinator: m.devices[0], Phase: m.phase, Connected: m.connected, Deadline: m.deadline, Reason: m.reason}
	if m.lease.Epoch != 0 {
		lease := m.lease
		s.Lease = &lease
	}
	if m.previous.Epoch != 0 {
		previous := m.previous
		s.Previous = &previous
	}
	if m.waiting != nil {
		waiting := *m.waiting
		s.Waiting = &waiting
		s.WaitingDeadline = m.waitingUntil
	}
	for index, id := range m.devices {
		state := NodeLocal
		switch m.phase {
		case Preparing:
			state = NodePreparing
			if id == m.lease.Source {
				state = NodeRequesting
			}
		case Active:
			state = NodeReceiving
			if id == m.lease.Source {
				state = NodeSending
			}
		case Recovering:
			if !m.restored[index] {
				state = NodeRecovering
			}
		}
		s.Nodes[index] = Node{ID: id, State: state, Paused: m.paused[index], Ready: m.ready[index], Restored: m.restored[index]}
	}
	return s
}
