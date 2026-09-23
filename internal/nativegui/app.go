package nativegui

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
	"myapp/internal/identity"
)

//go:embed assets/myapp-app.svg
var appIcon []byte

//go:embed assets/myapp-tray-template.svg
var trayIcon []byte

type Options struct {
	ConfigPath        string
	IdentityDirectory string
	DefaultDeviceName string
}

func Run(ctx context.Context, options Options) error {
	application := app.NewWithID("local.myapp.client")
	application.SetIcon(fyne.NewStaticResource("myapp-app.svg", appIcon))
	window := application.NewWindow("MyApp 控制中心")
	window.Resize(fyne.NewSize(820, 560))
	window.SetCloseIntercept(func() { window.Hide() })

	content, refresh := buildContent(options)
	window.SetContent(content)

	if desktopApp, ok := application.(desktop.App); ok {
		desktopApp.SetSystemTrayMenu(fyne.NewMenu("MyApp",
			fyne.NewMenuItem("打开控制中心", window.Show),
			fyne.NewMenuItem("刷新状态", refresh),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem("退出 MyApp", application.Quit),
		))
		desktopApp.SetSystemTrayIcon(fyne.NewStaticResource("myapp-tray.svg", trayIcon))
	}

	go func() {
		<-ctx.Done()
		application.Quit()
	}()
	window.Show()
	application.Run()
	return nil
}

func buildContent(options Options) (fyne.CanvasObject, func()) {
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	device := widget.NewLabel("")
	device.Wrapping = fyne.TextWrapWord
	role := widget.NewLabel("")
	listen := widget.NewLabel("")
	peers := container.NewVBox()
	refresh := func() {
		state, err := loadState(options)
		if err != nil {
			status.SetText("状态读取失败：" + err.Error())
			return
		}
		if !state.Ready {
			status.SetText(state.SetupReason)
			device.SetText("尚未初始化")
			role.SetText("请先使用 CLI 初始化身份和配置")
			listen.SetText("")
			peers.Objects = nil
			peers.Refresh()
			return
		}
		status.SetText("运行状态：配置已就绪")
		device.SetText(fmt.Sprintf("设备：%s\n设备 ID：%s", state.DeviceName, state.DeviceID))
		role.SetText("角色：" + string(state.Role))
		listen.SetText(fmt.Sprintf("监听地址：%s\n文字剪贴板：%s    文件复制：%s", state.Listen, enabledText(state.Clipboard), enabledText(state.Files)))
		peers.Objects = make([]fyne.CanvasObject, 0, len(state.Peers))
		for _, peer := range state.Peers {
			peers.Add(widget.NewLabel(peer.ID + "  ·  " + peer.Address))
		}
		if len(state.Peers) == 0 {
			peers.Add(widget.NewLabel("暂无已配置设备"))
		}
		peers.Refresh()
	}
	refreshButton := widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), refresh)
	header := container.NewBorder(nil, nil, nil, refreshButton, widget.NewLabelWithStyle("设备状态", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	main := container.NewVBox(header, status, widget.NewSeparator(), device, role, listen, widget.NewSeparator(), widget.NewLabelWithStyle("已配对设备", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), peers)
	refresh()
	return container.NewPadded(main), refresh
}

type state struct {
	Ready       bool          `json:"ready"`
	DeviceID    string        `json:"device_id"`
	DeviceName  string        `json:"device_name"`
	Role        config.Role   `json:"role"`
	Listen      string        `json:"listen_address"`
	Peers       []config.Peer `json:"peers"`
	Clipboard   bool          `json:"clipboard_enabled"`
	Files       bool          `json:"files_enabled"`
	SetupReason string        `json:"setup_reason"`
}

func loadState(options Options) (state, error) {
	cfg, cfgErr := config.Load(options.ConfigPath)
	identityValue, identityErr := identity.Load(options.IdentityDirectory)
	if cfgErr != nil || identityErr != nil {
		return state{SetupReason: setupReason(cfgErr, identityErr)}, nil
	}
	return state{Ready: true, DeviceID: identityValue.DeviceID, DeviceName: cfg.DeviceName, Role: cfg.Role, Listen: cfg.ListenAddress, Peers: cfg.Peers, Clipboard: cfg.Clipboard.TextEnabled, Files: cfg.Files.Enabled}, nil
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
