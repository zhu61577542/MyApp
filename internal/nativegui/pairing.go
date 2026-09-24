//go:build gui

package nativegui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
	"myapp/internal/pairing"
)

func openPairing(application fyne.App, options Options, refresh func()) {
	window := application.NewWindow("MyApp 配对设备")
	window.Resize(fyne.NewSize(600, 500))
	mode := widget.NewRadioGroup([]string{"监听模式（listen）", "连接模式（connect）"}, nil)
	mode.Horizontal = true
	mode.Required = true
	mode.SetSelected("连接模式（connect）")
	listenAddress := widget.NewEntry()
	listenAddress.SetText(":24800")
	connectAddress := widget.NewEntry()
	connectAddress.SetPlaceHolder("例如 192.168.1.20:24800")
	listenConfig := container.NewVBox(widget.NewLabel("监听地址（监听模式使用 :端口）"), listenAddress)
	connectConfig := container.NewVBox(widget.NewLabel("连接地址（连接模式使用 主机:端口）"), connectAddress)
	listenConfig.Hide()
	addressConfig := container.NewVBox(listenConfig, connectConfig)
	output := widget.NewLabel("输入对端地址后开始配对。双方显示相同配对码时再确认。")
	output.Wrapping = fyne.TextWrapWord
	start := widget.NewButton("开始配对", nil)
	confirm := widget.NewButton("确认配对码", nil)
	confirm.Disable()
	terminate := widget.NewButton("终止", nil)
	terminate.Disable()
	cancel := widget.NewButton("取消", window.Close)
	var command *exec.Cmd
	var input io.WriteCloser
	var stateMu sync.Mutex
	terminated := false

	appendOutput := func(line string) {
		if strings.TrimSpace(line) == "" {
			return
		}
		current := output.Text
		if current == "输入对端地址后开始配对。双方显示相同配对码时再确认。" {
			current = ""
		}
		output.SetText(strings.TrimSpace(current + "\n" + line))
	}
	stop := func() {
		stateMu.Lock()
		terminated = true
		cmd := command
		command = nil
		input = nil
		stateMu.Unlock()
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		confirm.Disable()
		terminate.Disable()
		start.Enable()
		mode.Enable()
		listenAddress.Enable()
		connectAddress.Enable()
	}
	window.SetCloseIntercept(func() {
		stop()
		window.Hide()
	})

	start.OnTapped = func() {
		selectedMode := "connect"
		address := connectAddress
		if mode.Selected == "监听模式（listen）" {
			selectedMode = "listen"
			address = listenAddress
		}
		if strings.TrimSpace(address.Text) == "" {
			if selectedMode == "listen" {
				address.SetText(":24800")
			} else {
				appendOutput("连接模式必须填写对端地址，例如 192.168.1.20:24800")
				return
			}
		}
		if selectedMode == "connect" {
			if err := pairing.ValidateConnectAddress(strings.TrimSpace(address.Text)); err != nil {
				appendOutput(err.Error())
				return
			}
		}
		executable, err := os.Executable()
		if err != nil {
			appendOutput("无法定位 MyApp：" + err.Error())
			return
		}
		args := []string{"pair", selectedMode, "--addr", strings.TrimSpace(address.Text), "--identity-dir", options.IdentityDirectory, "--trust-dir", options.TrustDirectory}
		cmd := exec.Command(executable, args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			appendOutput("创建配对输出失败：" + err.Error())
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			appendOutput("创建配对错误输出失败：" + err.Error())
			return
		}
		input, err = cmd.StdinPipe()
		if err != nil {
			appendOutput("创建配对输入失败：" + err.Error())
			return
		}
		stateMu.Lock()
		terminated = false
		command = cmd
		stateMu.Unlock()
		if err := cmd.Start(); err != nil {
			appendOutput("启动配对失败：" + err.Error())
			stop()
			return
		}
		start.Disable()
		terminate.Enable()
		mode.Disable()
		listenAddress.Disable()
		connectAddress.Disable()
		appendOutput("配对进程已启动，等待对端连接……")
		go func(cmd *exec.Cmd) {
			var lines []string
			var linesMu sync.Mutex
			var readers sync.WaitGroup
			scan := func(reader io.Reader) {
				defer readers.Done()
				scanner := bufio.NewScanner(reader)
				for scanner.Scan() {
					line := scanner.Text()
					linesMu.Lock()
					lines = append(lines, line)
					linesMu.Unlock()
					fyne.Do(func() {
						appendOutput(pairingOutputMessage(line))
						if strings.Contains(line, "配对码") {
							confirm.Enable()
						}
					})
				}
			}
			readers.Add(2)
			go scan(stdout)
			go scan(stderr)
			readers.Wait()
			err := cmd.Wait()
			fyne.Do(func() {
				linesMu.Lock()
				outputText := strings.Join(lines, "\n")
				linesMu.Unlock()
				stateMu.Lock()
				wasTerminated := terminated
				stateMu.Unlock()
				if err != nil {
					if wasTerminated {
						appendOutput("配对进程已终止")
					} else {
						appendOutput(pairingFailureMessage(outputText))
					}
				} else {
					appendOutput("配对完成")
					if strings.Contains(outputText, "配对完成:") {
						if savePairResult(options, outputText) == nil {
							appendOutput("已将设备加入主电脑配置")
							refresh()
						}
					}
				}
				stateMu.Lock()
				if command == cmd {
					command = nil
					input = nil
				}
				stateMu.Unlock()
				confirm.Disable()
				terminate.Disable()
				start.Enable()
				mode.Enable()
				listenAddress.Enable()
				connectAddress.Enable()
			})
		}(cmd)
	}
	mode.OnChanged = func(selected string) {
		if selected == "监听模式（listen）" {
			listenConfig.Show()
			connectConfig.Hide()
		} else {
			listenConfig.Hide()
			connectConfig.Show()
		}
		addressConfig.Refresh()
	}
	confirm.OnTapped = func() {
		if input == nil {
			return
		}
		if _, err := io.WriteString(input, "yes\n"); err != nil {
			appendOutput("确认失败：" + err.Error())
			return
		}
		confirm.Disable()
		appendOutput("已确认配对码，等待完成……")
	}
	terminate.OnTapped = func() {
		stateMu.Lock()
		cmd := command
		terminated = true
		stateMu.Unlock()
		if cmd == nil || cmd.Process == nil {
			appendOutput("当前没有运行中的配对进程")
			return
		}
		_ = cmd.Process.Kill()
		terminate.Disable()
		appendOutput("正在终止配对进程……")
	}
	content := container.NewVBox(
		widget.NewLabel("配对模式"), mode, addressConfig,
		container.NewHBox(start, confirm, terminate, cancel),
		output,
	)
	window.SetContent(container.NewPadded(content))
	window.Show()
}

func pairingOutputMessage(line string) string {
	if strings.Contains(strings.ToLower(line), "connection refused") {
		return "无法连接目标设备：对方没有开启配对监听，请在对方电脑选择“监听”模式，并检查端口和防火墙。"
	}
	if strings.Contains(strings.ToLower(line), "no route to host") || strings.Contains(strings.ToLower(line), "network is unreachable") {
		return "无法连接目标设备：当前网络不可达，请检查局域网地址和网络连接。"
	}
	if strings.Contains(strings.ToLower(line), "i/o timeout") || strings.Contains(strings.ToLower(line), "timed out") {
		return "连接目标设备超时：请检查对方是否开启配对监听，以及端口是否被防火墙拦截。"
	}
	return line
}

func pairingFailureMessage(output string) string {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "connection refused"):
		return "配对未完成：目标设备未开启监听或端口不可达。请先在对方电脑选择“监听”模式。"
	case strings.Contains(lower, "no route to host"), strings.Contains(lower, "network is unreachable"):
		return "配对未完成：网络不可达，请检查两台电脑是否在同一网络。"
	case strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "timed out"):
		return "配对未完成：连接超时，请检查监听状态、防火墙和端口配置。"
	default:
		return "配对未完成：请检查目标地址、监听状态、端口和防火墙；详细原因已写入日志。"
	}
}

func savePairResult(options Options, text string) error {
	parts := strings.SplitN(text, "设备 ID ", 2)
	if len(parts) != 2 {
		return errors.New("配对结果缺少设备 ID")
	}
	parts = strings.SplitN(parts[1], "，地址 ", 2)
	if len(parts) != 2 {
		return errors.New("配对结果缺少设备地址")
	}
	id := strings.TrimSpace(parts[0])
	address := strings.TrimSpace(parts[1])
	cfg, err := config.Load(options.ConfigPath)
	if err != nil {
		return err
	}
	if cfg.Role != config.RoleController {
		return errors.New("受控电脑配对成功后无需加入 peers")
	}
	for index := range cfg.Peers {
		if cfg.Peers[index].ID == id {
			cfg.Peers[index].Address = address
			return config.Save(options.ConfigPath, cfg)
		}
	}
	if len(cfg.Peers)+1 > cfg.MaxDevices {
		return fmt.Errorf("已达到最多 %d 台设备", cfg.MaxDevices)
	}
	cfg.Peers = append(cfg.Peers, config.Peer{ID: id, Address: address})
	return config.Save(options.ConfigPath, cfg)
}
