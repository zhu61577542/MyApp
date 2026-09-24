//go:build gui

package nativegui

import (
	"errors"
	"os"
	"os/exec"
	"sync"

	"myapp/internal/config"
)

type serviceManager struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	status string
}

func (m *serviceManager) start(options Options, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil && m.cmd.ProcessState == nil {
		return errors.New("MyApp 服务已经在运行")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{}
	if cfg.Role == config.RoleController {
		if len(cfg.Peers) == 0 {
			return errors.New("主电脑至少需要配置一台已配对设备")
		}
		args = []string{"control", "--config", options.ConfigPath, "--identity-dir", options.IdentityDirectory, "--trust-dir", options.TrustDirectory}
	} else {
		args = []string{"serve", "--config", options.ConfigPath, "--identity-dir", options.IdentityDirectory, "--trust-dir", options.TrustDirectory}
	}
	cmd := exec.Command(executable, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	m.cmd = cmd
	m.status = "运行中"
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
			if err != nil {
				m.status = "已停止：" + err.Error()
			} else {
				m.status = "已停止"
			}
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *serviceManager) stop() {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (m *serviceManager) snapshot() (running bool, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil && m.cmd.ProcessState == nil, m.status
}
