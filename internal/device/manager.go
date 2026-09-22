package device

import (
	"errors"
	"fmt"
	"sync"

	"myapp/internal/config"
)

var (
	ErrCapacity      = errors.New("设备数量已达到上限")
	ErrUnknownDevice = errors.New("设备不存在")
	ErrInvalidRole   = errors.New("设备角色组合无效")
	ErrTargetOffline = errors.New("目标设备离线")
	ErrNotController = errors.New("只有主电脑可以切换控制目标")
)

type Device struct {
	ID     string
	Name   string
	Role   config.Role
	Online bool
}

type Manager struct {
	mu       sync.RWMutex
	local    Device
	max      int
	peers    map[string]Device
	activeID string
}

func NewManager(local Device, max int) (*Manager, error) {
	if local.ID == "" || local.Name == "" || (local.Role != config.RoleController && local.Role != config.RoleAgent) {
		return nil, errors.New("本机设备信息无效")
	}
	if max < 2 || max > 3 {
		return nil, errors.New("设备上限必须在 2 到 3 之间")
	}
	return &Manager{local: local, max: max, peers: make(map[string]Device)}, nil
}

func (m *Manager) AddPeer(peer Device) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if peer.ID == "" || peer.Name == "" || peer.ID == m.local.ID {
		return errors.New("对端设备信息无效")
	}
	if _, exists := m.peers[peer.ID]; exists {
		return fmt.Errorf("设备已存在: %s", peer.ID)
	}
	if len(m.peers)+1 >= m.max {
		return ErrCapacity
	}
	if m.local.Role == config.RoleController && peer.Role != config.RoleAgent || m.local.Role == config.RoleAgent && peer.Role != config.RoleController {
		return ErrInvalidRole
	}
	if m.local.Role == config.RoleAgent && len(m.peers) > 0 {
		return ErrCapacity
	}
	m.peers[peer.ID] = peer
	return nil
}

func (m *Manager) SetOnline(id string, online bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	peer, exists := m.peers[id]
	if !exists {
		return ErrUnknownDevice
	}
	peer.Online = online
	m.peers[id] = peer
	if !online && m.activeID == id {
		m.activeID = ""
	}
	return nil
}

func (m *Manager) Activate(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.local.Role != config.RoleController {
		return ErrNotController
	}
	peer, exists := m.peers[id]
	if !exists {
		return ErrUnknownDevice
	}
	if !peer.Online {
		return ErrTargetOffline
	}
	m.activeID = id
	return nil
}

func (m *Manager) RecoverLocal() {
	m.mu.Lock()
	m.activeID = ""
	m.mu.Unlock()
}

func (m *Manager) Active() (Device, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	peer, ok := m.peers[m.activeID]
	return peer, ok
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.peers) + 1
}
