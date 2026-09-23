package clipboard

import (
	"errors"
	"sync"
	"time"
)

type Decision byte

const (
	Accepted Decision = iota + 1
	IgnoredPaused
	IgnoredDuplicate
	IgnoredStale
)

type Engine struct {
	mu          sync.Mutex
	deviceID    string
	version     uint64
	paused      bool
	lastHash    string
	seenVersion map[string]uint64
}

func NewEngine(deviceID string) (*Engine, error) {
	if deviceID == "" {
		return nil, errors.New("设备 ID 不能为空")
	}
	return &Engine{deviceID: deviceID, seenVersion: make(map[string]uint64)}, nil
}

func (e *Engine) SetPaused(paused bool) {
	e.mu.Lock()
	e.paused = paused
	e.mu.Unlock()
}

func (e *Engine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

func (e *Engine) Local(text string, now time.Time) (Entry, Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.paused {
		return Entry{}, IgnoredPaused, nil
	}
	hash := TextHash(text)
	if hash == e.lastHash {
		return Entry{}, IgnoredDuplicate, nil
	}
	e.version++
	entry, err := NewEntry(e.deviceID, e.version, text, now)
	if err != nil {
		e.version--
		return Entry{}, 0, err
	}
	e.lastHash = hash
	e.seenVersion[e.deviceID] = e.version
	return entry, Accepted, nil
}

func (e *Engine) Remote(entry Entry) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	decision := e.remoteDecisionLocked(entry)
	if decision == Accepted {
		e.commitRemoteLocked(entry)
	}
	return decision
}

func (e *Engine) ApplyRemote(entry Entry, write func(string) error) (Decision, error) {
	if write == nil {
		return 0, errors.New("剪贴板写入函数不能为空")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	decision := e.remoteDecisionLocked(entry)
	if decision != Accepted {
		return decision, nil
	}
	if err := write(entry.Text); err != nil {
		return 0, err
	}
	e.commitRemoteLocked(entry)
	return Accepted, nil
}

func (e *Engine) remoteDecisionLocked(entry Entry) Decision {
	if e.paused {
		return IgnoredPaused
	}
	if entry.Validate() != nil {
		return IgnoredStale
	}
	if entry.Origin == e.deviceID || entry.Version <= e.seenVersion[entry.Origin] {
		return IgnoredStale
	}
	if entry.Hash == e.lastHash {
		e.seenVersion[entry.Origin] = entry.Version
		return IgnoredDuplicate
	}
	return Accepted
}

func (e *Engine) commitRemoteLocked(entry Entry) {
	e.seenVersion[entry.Origin] = entry.Version
	e.lastHash = entry.Hash
}
