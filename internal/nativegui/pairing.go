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

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
)

func openPairing(application fyne.App, options Options, refresh func()) {
	window := application.NewWindow("MyApp 配对设备")
	window.Resize(fyne.NewSize(560, 420))
	mode := widget.NewSelect([]string{"listen", "connect"}, nil)
	mode.SetSelected("connect")
	address := widget.NewEntry()
	address.SetText(":24800")
	output := widget.NewLabel("输入对端地址后开始配对。双方显示相同配对码时再确认。")
	output.Wrapping = fyne.TextWrapWord
	start := widget.NewButton("开始配对", nil)
	confirm := widget.NewButton("确认配对码", nil)
	confirm.Disable()
	cancel := widget.NewButton("取消", window.Close)
	var command *exec.Cmd
	var input io.WriteCloser

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
		if command != nil && command.Process != nil {
			_ = command.Process.Kill()
		}
		command = nil
		input = nil
		confirm.Disable()
		start.Enable()
	}
	window.SetCloseIntercept(func() {
		stop()
		window.Hide()
	})

	start.OnTapped = func() {
		if strings.TrimSpace(address.Text) == "" {
			appendOutput("地址不能为空")
			return
		}
		executable, err := os.Executable()
		if err != nil {
			appendOutput("无法定位 MyApp：" + err.Error())
			return
		}
		args := []string{"pair", mode.Selected, "--addr", strings.TrimSpace(address.Text), "--identity-dir", options.IdentityDirectory, "--trust-dir", options.TrustDirectory}
		command = exec.Command(executable, args...)
		stdout, err := command.StdoutPipe()
		if err != nil {
			appendOutput("创建配对输出失败：" + err.Error())
			return
		}
		command.Stderr = os.Stderr
		input, err = command.StdinPipe()
		if err != nil {
			appendOutput("创建配对输入失败：" + err.Error())
			return
		}
		if err := command.Start(); err != nil {
			appendOutput("启动配对失败：" + err.Error())
			stop()
			return
		}
		start.Disable()
		appendOutput("配对进程已启动，等待对端连接……")
		go func() {
			var lines []string
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
				lines = append(lines, line)
				fyne.Do(func() {
					appendOutput(line)
					if strings.Contains(line, "配对码") {
						confirm.Enable()
					}
				})
			}
			err := command.Wait()
			fyne.Do(func() {
				if err != nil {
					appendOutput("配对失败：" + err.Error())
				} else {
					appendOutput("配对完成")
					if line := strings.Join(lines, "\n"); strings.Contains(line, "配对完成:") {
						if savePairResult(options, line) == nil {
							appendOutput("已将设备加入主电脑配置")
							refresh()
						}
					}
				}
				command = nil
				input = nil
				confirm.Disable()
				start.Enable()
			})
		}()
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
	content := container.NewVBox(
		widget.NewLabel("配对模式"), mode,
		widget.NewLabel("地址（监听模式使用 :端口，连接模式使用 主机:端口）"), address,
		container.NewHBox(start, confirm, cancel),
		output,
	)
	window.SetContent(container.NewPadded(content))
	window.Show()
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
