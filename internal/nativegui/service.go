//go:build gui

package nativegui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"myapp/internal/config"
	"myapp/internal/desktop"
)

type serviceManager struct {
	mu        sync.Mutex
	writeMu   sync.Mutex
	cmd       *exec.Cmd
	input     io.WriteCloser
	status    string
	view      desktop.Snapshot
	next      uint64
	pending   map[uint64]chan desktop.Message
	done      chan struct{}
	lastError string
	stopping  bool
}

func (m *serviceManager) start(options Options, cfg config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil {
		return errors.New("共享连接已在运行")
	}
	if cfg.Role == config.RoleController && len(cfg.Peers) == 0 {
		return errors.New("请先添加另一台电脑")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "desktop-service", "--config", options.ConfigPath, "--identity-dir", options.IdentityDirectory, "--trust-dir", options.TrustDirectory)
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		input.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		input.Close()
		return err
	}
	m.cmd = cmd
	m.input = input
	m.status = "正在启动"
	m.lastError = ""
	m.stopping = false
	m.view = desktop.Snapshot{Phase: "starting", Role: cfg.Role, Text: cfg.Clipboard.TextEnabled, Files: cfg.Files.Enabled, Accepting: true}
	m.pending = make(map[uint64]chan desktop.Message)
	m.done = make(chan struct{})
	done := m.done
	go m.collect(cmd, stdout, stderr, done)
	return nil
}

func (m *serviceManager) collect(cmd *exec.Cmd, stdout, stderr io.Reader, done chan struct{}) {
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 512<<10)
		for scanner.Scan() {
			var message desktop.Message
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil || message.Version != desktop.Version {
				m.mu.Lock()
				if m.cmd == cmd {
					m.lastError = "服务返回的状态协议无效"
				}
				m.mu.Unlock()
				_ = cmd.Process.Kill()
				return
			}
			m.mu.Lock()
			if m.cmd == cmd {
				if message.Snapshot.Revision >= m.view.Revision {
					m.view = message.Snapshot
					m.status = phaseLabel(message.Snapshot.Phase)
				}
				if reply := m.pending[message.RequestID]; reply != nil {
					select {
					case reply <- message:
					default:
					}
				}
			}
			m.mu.Unlock()
		}
		if err := scanner.Err(); err != nil {
			m.mu.Lock()
			if m.cmd == cmd {
				m.lastError = "服务状态读取失败：" + err.Error()
			}
			m.mu.Unlock()
			_ = cmd.Process.Kill()
		}
	}()
	go func() {
		defer readers.Done()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(os.Stderr, line)
			if strings.HasPrefix(line, "错误:") || strings.Contains(line, "会话结束:") {
				m.mu.Lock()
				if m.cmd == cmd {
					m.lastError = line
				}
				m.mu.Unlock()
			}
		}
	}()
	readers.Wait()
	err := cmd.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	defer close(done)
	if m.cmd != cmd {
		return
	}
	wasStopping := m.stopping
	m.cmd = nil
	m.input = nil
	m.view.Target = ""
	m.view.Phase = "stopped"
	for i := range m.view.Peers {
		m.view.Peers[i].Status = "offline"
	}
	for _, reply := range m.pending {
		select {
		case reply <- desktop.Message{Version: desktop.Version, Error: "共享服务已停止"}:
		default:
		}
	}
	if err != nil && !wasStopping {
		if m.lastError == "" {
			m.lastError = err.Error()
		}
		m.status = "启动或运行失败"
	} else {
		m.status = "已停止"
	}
}

func (m *serviceManager) command(ctx context.Context, command desktop.Command) error {
	m.mu.Lock()
	if m.cmd == nil || m.input == nil || m.stopping {
		m.mu.Unlock()
		return errors.New("共享服务未就绪")
	}
	m.next++
	command.ID = m.next
	command.Version = desktop.Version
	input := m.input
	cmd := m.cmd
	reply := make(chan desktop.Message, 1)
	m.pending[command.ID] = reply
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.cmd == cmd {
			delete(m.pending, command.ID)
		}
		m.mu.Unlock()
	}()
	m.writeMu.Lock()
	err := json.NewEncoder(input).Encode(command)
	m.writeMu.Unlock()
	if err != nil {
		return err
	}
	select {
	case response := <-reply:
		if response.Error != "" {
			return errors.New(response.Error)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *serviceManager) stopGracefully(ctx context.Context) error {
	m.mu.Lock()
	alreadyStopping := m.stopping && m.cmd != nil
	m.mu.Unlock()
	if alreadyStopping {
		return m.waitStopped(ctx)
	}
	if running, _ := m.snapshot(); !running {
		return nil
	}
	if err := m.command(ctx, desktop.Command{Action: "prepare_stop"}); err != nil {
		if running, _ := m.snapshot(); running {
			return err
		}
		return nil
	}
	m.mu.Lock()
	if m.cmd == nil {
		m.mu.Unlock()
		return nil
	}
	input := m.input
	done := m.done
	m.stopping = true
	m.status = "正在安全停止"
	m.next++
	id := m.next
	m.mu.Unlock()
	if input != nil {
		m.writeMu.Lock()
		_ = json.NewEncoder(input).Encode(desktop.Command{Version: desktop.Version, ID: id, Action: "stop"})
		_ = input.Close()
		m.writeMu.Unlock()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("服务尚未完成安全停止，可继续等待或明确选择强制结束")
	}
}

func (m *serviceManager) waitStopped(ctx context.Context) error {
	m.mu.Lock()
	done, running := m.done, m.cmd != nil
	m.mu.Unlock()
	if !running {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("服务尚未退出，请继续等待")
	}
}

func (m *serviceManager) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = m.stopGracefully(ctx)
}
func (m *serviceManager) forceStop() {
	m.mu.Lock()
	cmd := m.cmd
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
func (m *serviceManager) snapshot() (bool, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil, m.status
}
func (m *serviceManager) snapshotState() (desktop.Snapshot, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.view
	state.Peers = append([]desktop.Peer(nil), state.Peers...)
	state.Tasks = append([]desktop.Task(nil), state.Tasks...)
	return state, m.lastError
}

func phaseLabel(phase string) string {
	switch phase {
	case "starting":
		return "正在启动"
	case "listening":
		return "等待主电脑连接"
	case "connecting":
		return "正在连接"
	case "connected":
		return "连接已建立"
	case "reconnecting":
		return "正在重新连接"
	case "stopping":
		return "正在安全停止"
	default:
		return "已停止"
	}
}
