//go:build gui

package nativegui

import (
	"context"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
	"myapp/internal/logging"
)

func openSettings(application fyne.App, options Options, refresh func()) {
	window := application.NewWindow("MyApp 设置")
	view := newDesktopView(application, window, options, &serviceManager{}, &pairingManager{})
	view.cfg, _ = config.Load(options.ConfigPath)
	view.window = window
	window.Resize(fyne.NewSize(610, 680))
	window.SetContent(container.NewPadded(container.NewVScroll(view.settingsContent(refresh))))
	window.Show()
}

func (v *desktopView) settingsPage() fyne.CanvasObject {
	return v.settingsContent(func() { v.signature = ""; v.refresh(true) })
}

func (v *desktopView) settingsContent(afterSave func()) fyne.CanvasObject {
	cfg, err := config.Load(v.options.ConfigPath)
	if err != nil {
		return paragraph("无法读取设置：" + err.Error())
	}
	result := paragraph("连接相关设置保存后，会安全重新连接。外观与登录启动单独生效。")
	name := widget.NewEntry()
	name.SetText(cfg.DeviceName)
	role := widget.NewSelect([]string{"主电脑", "子电脑"}, nil)
	role.SetSelected(roleName(cfg.Role))
	appearance := widget.NewSelect([]string{"跟随系统", "浅色", "深色"}, nil)
	selected := "跟随系统"
	if v.prefs.Appearance == "light" {
		selected = "浅色"
	} else if v.prefs.Appearance == "dark" {
		selected = "深色"
	}
	appearance.SetSelected(selected)
	appearance.OnChanged = func(value string) {
		old := v.prefs.Appearance
		v.prefs.Appearance = "system"
		if value == "浅色" {
			v.prefs.Appearance = "light"
		} else if value == "深色" {
			v.prefs.Appearance = "dark"
		}
		if err := savePreferences(v.options, v.prefs); err != nil {
			v.prefs.Appearance = old
			result.SetText("外观未保存：" + err.Error())
			callback := appearance.OnChanged
			appearance.OnChanged = nil
			label := map[string]string{"light": "浅色", "dark": "深色", "system": "跟随系统"}[old]
			if label == "" {
				label = "跟随系统"
			}
			appearance.SetSelected(label)
			appearance.OnChanged = callback
			return
		}
		v.application.Settings().SetTheme(desktopTheme{appearance: v.prefs.Appearance})
	}
	startup := widget.NewCheck("登录后启动 MyApp（自动连接已信任电脑）", nil)
	enabled, startupErr := loginItemEnabled()
	startup.SetChecked(enabled)
	if startupErr != nil {
		result.SetText("启动项状态读取失败：" + startupErr.Error())
	}
	startup.OnChanged = func(value bool) {
		if err := setLoginItem(value, v.options); err != nil {
			result.SetText("启动项未更改：" + err.Error())
			callback := startup.OnChanged
			startup.OnChanged = nil
			startup.SetChecked(!value)
			startup.OnChanged = callback
		}
	}
	text := widget.NewCheck("文字剪贴板同步", nil)
	text.SetChecked(cfg.Clipboard.TextEnabled)
	files := widget.NewCheck("文件与文件夹共享", nil)
	files.SetChecked(cfg.Files.Enabled)
	listen := widget.NewEntry()
	listen.SetText(cfg.ListenAddress)
	loggingEnabled := widget.NewCheck("记录诊断日志", nil)
	loggingEnabled.SetChecked(cfg.Logging.Enabled)
	level := widget.NewSelect([]string{"debug", "info", "warn", "error"}, nil)
	level.SetSelected(string(cfg.Logging.Level))
	loggingEnabled.OnChanged = func(enabled bool) {
		if enabled {
			level.Enable()
		} else {
			level.Disable()
		}
	}
	loggingEnabled.OnChanged(loggingEnabled.Checked)
	var save *widget.Button
	save = primary("保存设置", func() {
		newRole := config.RoleAgent
		if role.Selected == "主电脑" {
			newRole = config.RoleController
		}
		doSave := func() {
			if v.busy {
				return
			}
			v.busy = true
			save.Disable()
			result.SetText("正在应用…")
			deviceName, listenAddress, logLevel := strings.TrimSpace(name.Text), strings.TrimSpace(listen.Text), config.LogLevel(level.Selected)
			textEnabled, filesEnabled, logsEnabled := text.Checked, files.Checked, loggingEnabled.Checked
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
				defer cancel()
				running, _ := v.services.snapshot()
				var failure error
				if v.pairings.snapshot().Running {
					failure = v.pairings.stopAndWait(ctx)
				}
				if running && failure == nil {
					failure = v.services.stopGracefully(ctx)
				}
				var updated config.Config
				if failure == nil {
					configMu.Lock()
					updated, failure = config.Load(v.options.ConfigPath)
					if failure == nil {
						updated.DeviceName = deviceName
						updated.Role = newRole
						if newRole == config.RoleAgent {
							updated.Peers = nil
						}
						updated.ListenAddress = listenAddress
						updated.Clipboard.TextEnabled = textEnabled
						updated.Files.Enabled = filesEnabled
						updated.Logging.Enabled = logsEnabled
						updated.Logging.Level = logLevel
						failure = config.Save(v.options.ConfigPath, updated)
					}
					configMu.Unlock()
				}
				if failure == nil && running && (updated.Role == config.RoleAgent || len(updated.Peers) > 0) {
					failure = v.services.start(v.options, updated)
				}
				fyne.Do(func() {
					v.busy = false
					save.Enable()
					if failure != nil {
						result.SetText("未能完成应用：" + failure.Error())
					} else {
						result.SetText("设置已保存；连接状态请查看设备页。")
						v.permissionTime = time.Time{}
						if afterSave != nil {
							afterSave()
						}
					}
				})
			}()
		}
		active := false
		for _, task := range v.view.Tasks {
			if task.Active() {
				active = true
			}
		}
		if running, _ := v.services.snapshot(); running || newRole != cfg.Role || active || v.pairings.snapshot().Running {
			dialog.ShowConfirm("应用设置并重新连接？", "当前控制与未完成的配对将停止；传输会先请求取消。切换为子电脑会移除主电脑的连接配置，已确认的信任记录保留。", func(confirmed bool) {
				if confirmed {
					doSave()
				}
			}, v.window)
		} else {
			doSave()
		}
	})
	advanced := container.NewVBox(widget.NewForm(widget.NewFormItem("监听地址", listen)), loggingEnabled, widget.NewForm(widget.NewFormItem("日志最低级别", level)), secondary("日志位置："+logging.DefaultPath()), widget.NewButton("导出日志", func() {
		v.exportLogDialog(result.SetText)
	}))
	version := v.options.Version
	if version == "" {
		version = "dev"
	}
	advanced.Add(secondary("MyApp " + version + " · 构建 " + v.options.Commit))
	return container.NewVBox(pageHeader("设置", "更改这台电脑的用途与共享偏好。"), sectionTitle("这台电脑"), surface(widget.NewForm(widget.NewFormItem("设备名称", name), widget.NewFormItem("使用方式", role))), sectionTitle("共享偏好"), surface(container.NewVBox(text, files, secondary("运行时快速开关也可在设备页操作。"))), sectionTitle("外观与启动"), surface(container.NewVBox(widget.NewForm(widget.NewFormItem("外观", appearance)), startup, secondary("已信任电脑会自动连接，并自动准备跨屏切换。"))), sectionTitle("快捷键"), surface(container.NewVBox(paragraph("结束控制："+emergencyShortcut()), secondary("切换目标："+targetShortcut()))), widget.NewAccordion(widget.NewAccordionItem("高级与诊断", advanced)), result, container.NewHBox(save), secondary("MyApp · 局域网协同工具"))
}
