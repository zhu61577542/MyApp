package app

import (
	"errors"
	"sync"

	"myapp/internal/config"
)

type ControlState string

const (
	StateLocal      ControlState = "local"
	StateEntering   ControlState = "entering-remote"
	StateRemote     ControlState = "remote"
	StateRecovering ControlState = "recovering"
)

var (
	ErrWrongState    = errors.New("当前控制状态不允许此操作")
	ErrStaleAction   = errors.New("控制操作已经失效")
	ErrNotController = errors.New("受控电脑不能发起远端控制")
)

type Transition struct {
	Generation uint64
	TargetID   string
	State      ControlState
	Reason     string
}

type ControlMachine struct {
	mu         sync.RWMutex
	role       config.Role
	state      ControlState
	targetID   string
	generation uint64
	reason     string
}

func NewControlMachine(role config.Role) (*ControlMachine, error) {
	if role != config.RoleController && role != config.RoleAgent {
		return nil, errors.New("设备角色无效")
	}
	return &ControlMachine{role: role, state: StateLocal}, nil
}

func (m *ControlMachine) BeginEnter(targetID string) (Transition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.role != config.RoleController {
		return Transition{}, ErrNotController
	}
	if m.state != StateLocal || targetID == "" {
		return Transition{}, ErrWrongState
	}
	m.generation++
	m.state, m.targetID, m.reason = StateEntering, targetID, ""
	return m.transition(), nil
}

func (m *ControlMachine) ConfirmEnter(generation uint64, targetID string) (Transition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation != m.generation || targetID != m.targetID {
		return Transition{}, ErrStaleAction
	}
	if m.state != StateEntering {
		return Transition{}, ErrWrongState
	}
	m.state = StateRemote
	return m.transition(), nil
}

func (m *ControlMachine) FailEnter(generation uint64, reason string) (Transition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation != m.generation {
		return Transition{}, ErrStaleAction
	}
	if m.state != StateEntering {
		return Transition{}, ErrWrongState
	}
	m.state, m.targetID, m.reason = StateLocal, "", reason
	return m.transition(), nil
}

func (m *ControlMachine) BeginRecovery(reason string) (Transition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateRemote && m.state != StateEntering {
		return Transition{}, ErrWrongState
	}
	m.state, m.reason = StateRecovering, reason
	return m.transition(), nil
}

func (m *ControlMachine) LocalRestored(generation uint64) (Transition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation != m.generation {
		return Transition{}, ErrStaleAction
	}
	if m.state != StateRecovering {
		return Transition{}, ErrWrongState
	}
	release := m.transition()
	m.state, m.targetID = StateLocal, ""
	return release, nil
}

func (m *ControlMachine) Snapshot() Transition {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.transition()
}

func (m *ControlMachine) transition() Transition {
	return Transition{Generation: m.generation, TargetID: m.targetID, State: m.state, Reason: m.reason}
}
