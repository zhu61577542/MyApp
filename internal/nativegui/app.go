//go:build gui

package nativegui

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
	"myapp/internal/identity"
	"myapp/internal/logging"
)

//go:embed assets/myapp-app.svg
var appIcon []byte

//go:embed assets/myapp-tray-template.svg
var trayIcon []byte

type Options struct {
	ConfigPath        string
	IdentityDirectory string
	TrustDirectory    string
	DefaultDeviceName string
}

func Run(ctx context.Context, options Options) error {
	application := app.NewWithID("local.myapp.client")
	application.SetIcon(fyne.NewStaticResource("myapp-app.svg", appIcon))
	window := application.NewWindow("MyApp 控制中心")
	window.Resize(fyne.NewSize(820, 560))
	window.SetCloseIntercept(func() { window.Hide() })

	services := &serviceManager{status: "未启动"}
	defer services.stop()
	content, refresh := buildContent(application, options, services)
	window.SetContent(content)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fyne.Do(refresh)
			}
		}
	}()

	if desktopApp, ok := application.(desktop.App); ok {
		quitItem := fyne.NewMenuItem("退出 MyApp", application.Quit)
		quitItem.IsQuit = true
		desktopApp.SetSystemTrayMenu(fyne.NewMenu("MyApp",
			fyne.NewMenuItem("打开控制中心", window.Show),
			fyne.NewMenuItem("刷新状态", refresh),
			fyne.NewMenuItemSeparator(),
			quitItem,
		))
		desktopApp.SetSystemTrayIcon(fyne.NewStaticResource("myapp-tray.svg", trayIcon))
	}

	go func() {
		<-ctx.Done()
		services.stop()
		application.Quit()
	}()
	window.Show()
	application.Run()
	return nil
}

func buildContent(application fyne.App, options Options, services *serviceManager) (fyne.CanvasObject, func()) {
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	device := widget.NewLabel("")
	device.Wrapping = fyne.TextWrapWord
	role := widget.NewLabel("")
	listen := widget.NewLabel("")
	peers := container.NewVBox()
	deviceName := widget.NewEntry()
	deviceName.SetText(options.DefaultDeviceName)
	roleSelect := widget.NewSelect([]string{string(config.RoleAgent), string(config.RoleController)}, nil)
	roleSelect.SetSelected(string(config.RoleAgent))
	setupStatus := widget.NewLabel("")
	setupButton := widget.NewButton("初始化设备", nil)
	setupBox := container.NewVBox(widget.NewLabelWithStyle("首次启动", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), widget.NewLabel("设备名称"), deviceName, widget.NewLabel("设备角色"), roleSelect, setupButton, setupStatus)
	settingsButton := widget.NewButton("设置", nil)
	pairButton := widget.NewButton("配对", nil)
	serviceStatus := widget.NewLabel("")
	serviceButton := widget.NewButton("启动服务", nil)
	var refresh func()
	refresh = func() {
		state, err := loadState(options)
		if err != nil {
			status.SetText("状态读取失败：" + err.Error())
			return
		}
		if !state.Ready {
			settingsButton.Disable()
			pairButton.Disable()
			serviceButton.Disable()
			serviceStatus.SetText("服务未启动")
			status.SetText(state.SetupReason)
			device.SetText("尚未初始化")
			role.SetText("请先使用 CLI 初始化身份和配置")
			listen.SetText("")
			peers.Objects = nil
			peers.Refresh()
			setupBox.Show()
			return
		}
		setupBox.Hide()
		pairButton.Enable()
		serviceButton.Enable()
		running, serviceState := services.snapshot()
		if running {
			serviceButton.SetText("停止服务")
		} else {
			serviceButton.SetText("启动服务")
		}
		serviceStatus.SetText("服务状态：" + serviceState)
		status.SetText("运行状态：配置已就绪")
		device.SetText(fmt.Sprintf("设备：%s\n设备 ID：%s", state.DeviceName, state.DeviceID))
		role.SetText("角色：" + string(state.Role))
		listen.SetText(fmt.Sprintf("监听地址：%s\n文字剪贴板：%s    文件复制：%s\n日志：%s（级别：%s）", state.Listen, enabledText(state.Clipboard), enabledText(state.Files), enabledText(state.Logging), state.LoggingLevel))
		peers.Objects = make([]fyne.CanvasObject, 0, len(state.Peers))
		for _, peer := range state.Peers {
			peerID := peer.ID
			remove := widget.NewButton("删除", func() {
				if err := removePeer(options, peerID); err != nil {
					status.SetText("删除设备失败：" + err.Error())
					return
				}
				status.SetText("设备已删除，重启服务后生效")
				refresh()
			})
			peers.Add(container.NewBorder(nil, nil, nil, remove, widget.NewLabel(peer.ID+"  ·  "+peer.Address)))
		}
		if len(state.Peers) == 0 {
			peers.Add(widget.NewLabel("暂无已配置设备"))
		}
		peers.Refresh()
		settingsButton.Enable()
	}
	pairButton.OnTapped = func() { openPairing(application, options, refresh) }
	serviceButton.OnTapped = func() {
		running, _ := services.snapshot()
		if running {
			services.stop()
			serviceStatus.SetText("服务状态：正在停止")
			serviceButton.SetText("启动服务")
			return
		}
		cfg, err := config.Load(options.ConfigPath)
		if err != nil {
			serviceStatus.SetText("启动失败：" + err.Error())
			return
		}
		if err := services.start(options, cfg); err != nil {
			serviceStatus.SetText("启动失败：" + err.Error())
			return
		}
		serviceButton.SetText("停止服务")
		serviceStatus.SetText("服务状态：运行中")
	}
	showSettings := func() { openSettings(application, options, refresh) }
	settingsButton.OnTapped = showSettings
	setupButton.OnTapped = func() {
		name := strings.TrimSpace(deviceName.Text)
		role := config.Role(roleSelect.Selected)
		generated, err := identity.Generate(name, time.Now())
		if err == nil {
			err = identity.SaveNew(options.IdentityDirectory, generated)
		}
		if err == nil {
			err = config.SaveNew(options.ConfigPath, config.Default(name, role))
		}
		if err != nil {
			setupStatus.SetText("初始化失败：" + err.Error())
			return
		}
		setupStatus.SetText("初始化完成")
		refresh()
	}
	refreshButton := widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), refresh)
	actions := container.NewHBox(pairButton, settingsButton, refreshButton)
	header := container.NewBorder(nil, nil, nil, actions, widget.NewLabelWithStyle("设备状态", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	serviceBox := container.NewHBox(serviceButton, serviceStatus)
	main := container.NewVBox(header, status, setupBox, serviceBox, widget.NewSeparator(), device, role, listen, widget.NewSeparator(), widget.NewLabelWithStyle("已配对设备", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), peers)
	refresh()
	return container.NewPadded(main), refresh
}

func removePeer(options Options, peerID string) error {
	cfg, err := config.Load(options.ConfigPath)
	if err != nil {
		return err
	}
	filtered := cfg.Peers[:0]
	for _, peer := range cfg.Peers {
		if peer.ID != peerID {
			filtered = append(filtered, peer)
		}
	}
	if len(filtered) == len(cfg.Peers) {
		return errors.New("设备不存在")
	}
	cfg.Peers = filtered
	return config.Save(options.ConfigPath, cfg)
}

func openSettings(application fyne.App, options Options, refresh func()) {
	cfg, err := config.Load(options.ConfigPath)
	window := application.NewWindow("MyApp 设置")
	window.Resize(fyne.NewSize(560, 600))
	if err != nil {
		window.SetContent(container.NewPadded(container.NewVBox(widget.NewLabel("无法读取配置：" + err.Error()))))
		window.Show()
		return
	}

	name := widget.NewEntry()
	name.SetText(cfg.DeviceName)
	role := widget.NewSelect([]string{string(config.RoleAgent), string(config.RoleController)}, nil)
	role.SetSelected(string(cfg.Role))
	listen := widget.NewEntry()
	listen.SetText(cfg.ListenAddress)
	clipboard := widget.NewCheck("启用文字剪贴板同步", nil)
	clipboard.SetChecked(cfg.Clipboard.TextEnabled)
	files := widget.NewCheck("启用文件复制", nil)
	files.SetChecked(cfg.Files.Enabled)
	loggingEnabled := widget.NewCheck("启用日志记录", nil)
	loggingEnabled.SetChecked(cfg.Logging.Enabled)
	loggingLevel := widget.NewSelect([]string{string(config.LogLevelDebug), string(config.LogLevelInfo), string(config.LogLevelWarn), string(config.LogLevelError)}, nil)
	loggingLevel.SetSelected(string(cfg.Logging.Level))
	loggingEnabled.OnChanged = func(enabled bool) {
		if enabled {
			loggingLevel.Enable()
		} else {
			loggingLevel.Disable()
		}
	}
	loggingEnabled.OnChanged(loggingEnabled.Checked)
	result := widget.NewLabel("")
	exportButton := widget.NewButton("导出日志", func() {
		path, exportErr := exportLogs()
		if exportErr != nil {
			result.SetText("导出日志失败：" + exportErr.Error())
			return
		}
		result.SetText("日志已导出：" + path)
	})
	save := widget.NewButton("保存设置", func() {
		updated := cfg
		updated.DeviceName = strings.TrimSpace(name.Text)
		updated.Role = config.Role(role.Selected)
		updated.ListenAddress = strings.TrimSpace(listen.Text)
		updated.Clipboard.TextEnabled = clipboard.Checked
		updated.Files.Enabled = files.Checked
		updated.Logging.Enabled = loggingEnabled.Checked
		updated.Logging.Level = config.LogLevel(loggingLevel.Selected)
		if err := config.Save(options.ConfigPath, updated); err != nil {
			result.SetText("保存失败：" + err.Error())
			return
		}
		cfg = updated
		result.SetText("保存成功。重启 MyApp 服务后生效。")
		refresh()
	})
	closeButton := widget.NewButton("关闭", window.Close)
	buttons := container.NewHBox(save, closeButton)
	content := container.NewVBox(
		widget.NewLabelWithStyle("设备设置", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("设备名称"), name,
		widget.NewLabel("设备角色"), role,
		widget.NewLabel("监听地址"), listen,
		clipboard, files,
		widget.NewLabel("日志文件："+logging.DefaultPath()),
		loggingEnabled,
		widget.NewLabel("最低输出级别"), loggingLevel,
		result,
		container.NewHBox(exportButton, buttons),
	)
	window.SetContent(container.NewPadded(content))
	window.Show()
}

type state struct {
	Ready        bool          `json:"ready"`
	DeviceID     string        `json:"device_id"`
	DeviceName   string        `json:"device_name"`
	Role         config.Role   `json:"role"`
	Listen       string        `json:"listen_address"`
	Peers        []config.Peer `json:"peers"`
	Clipboard    bool          `json:"clipboard_enabled"`
	Files        bool          `json:"files_enabled"`
	Logging      bool          `json:"logging_enabled"`
	LoggingLevel string        `json:"logging_level"`
	SetupReason  string        `json:"setup_reason"`
}

func loadState(options Options) (state, error) {
	cfg, cfgErr := config.Load(options.ConfigPath)
	identityValue, identityErr := identity.Load(options.IdentityDirectory)
	if cfgErr != nil || identityErr != nil {
		return state{SetupReason: setupReason(cfgErr, identityErr)}, nil
	}
	return state{Ready: true, DeviceID: identityValue.DeviceID, DeviceName: cfg.DeviceName, Role: cfg.Role, Listen: cfg.ListenAddress, Peers: cfg.Peers, Clipboard: cfg.Clipboard.TextEnabled, Files: cfg.Files.Enabled, Logging: cfg.Logging.Enabled, LoggingLevel: string(cfg.Logging.Level)}, nil
}

func setupReason(cfgErr, identityErr error) string {
	if errors.Is(cfgErr, os.ErrNotExist) && errors.Is(identityErr, os.ErrNotExist) {
		return "尚未初始化设备身份和配置"
	}
	if errors.Is(cfgErr, os.ErrNotExist) {
		return "尚未初始化配置"
	}
	if errors.Is(identityErr, os.ErrNotExist) {
		return "尚未初始化设备身份"
	}
	return "配置或身份文件无效，请先通过 CLI 修复"
}

func enabledText(enabled bool) string {
	if enabled {
		return "已启用"
	}
	return "已暂停"
}
