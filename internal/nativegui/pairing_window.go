//go:build gui

package nativegui

import (
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/pairing"
)

func openPairing(application fyne.App, options Options, manager *pairingManager) {
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
	var localMessage string

	var render func()
	render = func() {
		snapshot := manager.snapshot()
		if snapshot.Running {
			if snapshot.Mode == "listen" {
				mode.SetSelected("监听模式（listen）")
				listenAddress.SetText(snapshot.Address)
			} else {
				mode.SetSelected("连接模式（connect）")
				connectAddress.SetText(snapshot.Address)
			}
			mode.Disable()
			listenAddress.Disable()
			connectAddress.Disable()
			start.Disable()
			terminate.Enable()
		} else {
			mode.Enable()
			listenAddress.Enable()
			connectAddress.Enable()
			start.Enable()
			terminate.Disable()
		}
		if snapshot.ConfirmAvailable && snapshot.Running {
			confirm.Enable()
		} else {
			confirm.Disable()
		}

		lines := make([]string, 0, len(snapshot.Lines)+3)
		for _, line := range snapshot.Lines {
			lines = append(lines, pairingOutputMessage(line))
		}
		if snapshot.Status != "" {
			lines = append(lines, snapshot.Status)
		}
		if localMessage != "" {
			lines = append(lines, localMessage)
		}
		if len(lines) == 0 {
			output.SetText("输入对端地址后开始配对。双方显示相同配对码时再确认。")
		} else {
			output.SetText(strings.Join(lines, "\n"))
		}
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
	start.OnTapped = func() {
		selectedMode := "connect"
		address := strings.TrimSpace(connectAddress.Text)
		if mode.Selected == "监听模式（listen）" {
			selectedMode = "listen"
			address = strings.TrimSpace(listenAddress.Text)
			if address == "" {
				address = ":24800"
			}
		} else if address == "" {
			localMessage = "连接模式必须填写对端地址，例如 192.168.1.20:24800"
			render()
			return
		}
		if selectedMode == "connect" {
			if err := pairing.ValidateConnectAddress(address); err != nil {
				localMessage = err.Error()
				render()
				return
			}
		}
		localMessage = ""
		if err := manager.start(options, selectedMode, address); err != nil {
			localMessage = "启动配对失败：" + err.Error()
		}
		render()
	}
	confirm.OnTapped = func() {
		if err := manager.confirm(); err != nil {
			localMessage = "确认失败：" + err.Error()
		} else {
			localMessage = "已确认配对码，等待完成……"
		}
		render()
	}
	terminate.OnTapped = func() {
		manager.stop()
		localMessage = "正在终止配对进程……"
		render()
	}

	content := container.NewVBox(
		widget.NewLabel("配对模式"), mode, addressConfig,
		container.NewHBox(start, confirm, terminate, cancel),
		output,
	)
	window.SetContent(container.NewPadded(content))

	updatesDone := make(chan struct{})
	var closeUpdates sync.Once
	window.SetCloseIntercept(func() {
		closeUpdates.Do(func() { close(updatesDone) })
		window.Hide()
	})
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fyne.Do(render)
			case <-updatesDone:
				return
			}
		}
	}()
	render()
	window.Show()
}
