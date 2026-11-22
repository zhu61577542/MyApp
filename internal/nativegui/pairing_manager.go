//go:build gui

package nativegui

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"myapp/internal/config"
	"myapp/internal/pairing"
)

type pairingSnapshot struct {
	Running          bool
	Mode             string
	Address          string
	Lines            []string
	Status           string
	ConfirmAvailable bool
	Code             string
	RemoteName       string
	Confirmed        bool
	Deadline         time.Time
}

// 配对进程独立于窗口运行。
type pairingManager struct {
	mu              sync.Mutex
	cmd             *exec.Cmd
	input           io.WriteCloser
	mode            string
	address         string
	status          string
	lines           []string
	terminated      bool
	confirmed       bool
	started         time.Time
	window          fyne.Window
	showWindow      func()
	advertiseCancel context.CancelFunc
}

func (m *pairingManager) start(options Options, mode, address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != nil {
		return errors.New("配对进程已在运行")
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"pair", mode, "--addr", address}
	if cfg, err := config.Load(options.ConfigPath); err == nil {
		_, port, splitErr := net.SplitHostPort(cfg.ListenAddress)
		if splitErr != nil {
			return splitErr
		}
		if _, err := strconv.Atoi(port); err != nil {
			return err
		}
		args = append(args, "--desktop", "--service-port", port)
	} else {
		return err
	}
	if options.IdentityDirectory != "" {
		args = append(args, "--identity-dir", options.IdentityDirectory)
	}
	if options.TrustDirectory != "" {
		args = append(args, "--trust-dir", options.TrustDirectory)
	}
	cmd := exec.Command(executable, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	m.cmd = cmd
	m.input = input
	m.mode = mode
	m.address = address
	m.status = "配对进程已启动，等待对端连接……"
	m.lines = nil
	m.terminated = false
	m.confirmed = false
	m.started = time.Now()

	go m.collect(options, cmd, stdout, stderr)
	return nil
}

func (m *pairingManager) collect(options Options, cmd *exec.Cmd, stdout, stderr io.Reader) {
	var readers sync.WaitGroup
	read := func(reader io.Reader) {
		defer readers.Done()
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			m.mu.Lock()
			m.lines = append(m.lines, line)
			if len(m.lines) > 64 {
				m.lines = append([]string(nil), m.lines[len(m.lines)-64:]...)
			}
			m.mu.Unlock()
			if strings.Contains(line, "等待配对连接:") {
				m.beginAdvertising(options, cmd, line)
			}
		}
	}
	readers.Add(2)
	go read(stdout)
	go read(stderr)
	readers.Wait()
	err := cmd.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd != cmd {
		return
	}
	if m.advertiseCancel != nil {
		m.advertiseCancel()
		m.advertiseCancel = nil
	}
	if m.terminated {
		m.status = "配对进程已终止"
	} else if err != nil {
		m.status = "配对未完成"
	} else {
		m.status = "配对完成"
		if err := savePairResult(options, strings.Join(m.lines, "\n")); err == nil {
			m.status = "配对完成，已将设备加入主电脑配置"
		} else if err.Error() != "受控电脑配对成功后无需加入 peers" {
			m.status = "配对完成，但保存设备信息失败：" + err.Error()
		}
	}
	m.cmd = nil
	m.input = nil
}

func (m *pairingManager) stop() {
	m.mu.Lock()
	cmd := m.cmd
	if cmd != nil {
		m.terminated = true
	}
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (m *pairingManager) stopAndWait(ctx context.Context) error {
	m.stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for m.snapshot().Running {
		select {
		case <-ctx.Done():
			return errors.New("配对进程尚未结束，请稍后重试")
		case <-ticker.C:
		}
	}
	return nil
}

func (m *pairingManager) confirm() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	input := m.input
	if m.confirmed {
		return errors.New("本机已经确认，正在等待另一台电脑")
	}
	if !m.started.IsZero() && time.Now().After(m.started.Add(pairing.OfferLifetime)) {
		return errors.New("配对已超时，请重新开始")
	}
	if input == nil {
		return errors.New("当前没有等待确认的配对码")
	}
	_, err := io.WriteString(input, "yes\n")
	if err == nil {
		m.confirmed = true
	}
	return err
}

func (m *pairingManager) snapshot() pairingSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	lines := append([]string(nil), m.lines...)
	running := m.cmd != nil
	confirmAvailable := false
	code, name := "", ""
	for _, line := range lines {
		if strings.Contains(line, "配对码") {
			confirmAvailable = true
			if found := pairCodePattern.FindStringSubmatch(line); len(found) == 2 {
				code = found[1]
			}
		}
		if strings.HasPrefix(line, "远端设备:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "远端设备:"))
		}
	}
	if m.confirmed {
		confirmAvailable = false
	}
	return pairingSnapshot{
		Running:          running,
		Mode:             m.mode,
		Address:          m.address,
		Lines:            lines,
		Status:           m.status,
		ConfirmAvailable: confirmAvailable,
		Code:             code, RemoteName: name, Confirmed: m.confirmed, Deadline: pairingDeadline(m.started),
	}
}

var pairCodePattern = regexp.MustCompile(`配对码:\s*(\d{6})`)

func pairingDeadline(start time.Time) time.Time {
	if start.IsZero() {
		return time.Time{}
	}
	return start.Add(pairing.OfferLifetime)
}
