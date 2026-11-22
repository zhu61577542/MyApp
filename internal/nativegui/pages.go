//go:build gui

package nativegui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/logging"
)

func pageHeader(title, subtitle string, actions ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewBorder(nil, nil, nil, container.NewHBox(actions...), container.NewVBox(heading(title), secondary(subtitle)))
}

func (v *desktopView) devicesPage() fyne.CanvasObject {
	add := primary("添加电脑", func() { openPairing(v.application, v.options, v.pairings) })
	if len(v.records)+1 >= v.cfg.MaxDevices {
		add.Disable()
	}
	more := quiet("更多", v.showConnectionActions)
	title, copy, action, enabled := v.controlSummary()
	control := primary(action, func() { v.performMainAction() })
	if !enabled || v.busy {
		control.Disable()
	}
	status := surface(container.NewBorder(nil, nil, nil, control, container.NewVBox(sectionTitle(title), paragraph(copy))))
	if v.view.Error != "" {
		status = container.NewVBox(status, secondary("最近一次连接信息："+shortError(v.view.Error)))
	}
	board := v.deviceBoard()
	var layoutAction fyne.CanvasObject = secondary("位置由主电脑管理")
	if v.cfg.Role == config.RoleController {
		label := "调整位置"
		if v.layoutEditing {
			label = "保存位置"
		}
		layoutAction = quiet(label, v.toggleLayout)
	}
	positionHeader := container.NewBorder(nil, nil, nil, layoutAction, sectionTitle(fmt.Sprintf("电脑位置 · %d / %d 台，包含本机", len(v.records)+1, v.cfg.MaxDevices)))
	position := container.NewVBox(positionHeader, board, secondary("控制期间通过 "+targetShortcut()+" 切换目标；屏幕边缘自动切换尚未接入。"))
	if v.layoutEditing {
		controls := container.NewVBox(secondary("拖动电脑屏幕，或使用位置选项。编辑期间键鼠留在本机。"))
		for _, peer := range v.cfg.Peers {
			peer := peer
			choices := container.NewGridWithColumns(4)
			for _, position := range []string{"left", "right", "up", "down"} {
				position := position
				button := quiet(positionLabel(position), func() { v.layoutDraft[peer.ID] = position; v.refresh(true) })
				if v.layoutDraft[peer.ID] == position {
					button.Importance = widget.HighImportance
				}
				choices.Add(button)
			}
			controls.Add(container.NewVBox(sectionTitle(peerName(peer.ID, v.view.Peers, v.records)), choices))
		}
		controls.Add(quiet("取消位置调整", func() { v.layoutEditing = false; v.layoutDraft = make(map[string]string); v.refresh(true) }))
		position.Add(controls)
	}
	textValue, filesValue := v.cfg.Clipboard.TextEnabled, v.cfg.Files.Enabled
	running, _ := v.services.snapshot()
	if running {
		textValue = v.view.Text
		filesValue = v.view.Files
	}
	text := widget.NewCheck("文字剪贴板", nil)
	text.SetChecked(textValue)
	text.OnChanged = func(value bool) { v.changeSharing("text", value) }
	files := widget.NewCheck("文件与文件夹", nil)
	files.SetChecked(filesValue)
	files.OnChanged = func(value bool) { v.changeSharing("files", value) }
	if v.busy || v.view.Paused {
		text.Disable()
		files.Disable()
	}
	note := "双向复制纯文字；文件接收并校验后，在目标电脑粘贴。"
	if !running {
		note = "当前未连接。这里保存的是下次连接时的共享偏好。"
	}
	if v.view.Paused {
		note = "新共享内容已暂停；已批准的文件任务可继续，也可在传输页取消。"
	}
	sharing := surface(container.NewVBox(container.NewGridWithColumns(2, text, files), secondary(note)))
	return container.NewVBox(pageHeader("设备", "共享键鼠，让电脑协同工作。", more, add), status, position, sectionTitle("共享内容"), sharing)
}

func targetShortcut() string {
	if runtime.GOOS == "darwin" {
		return "Control+Option+1/2"
	}
	return "Ctrl+Alt+1/2"
}
func (v *desktopView) controlSummary() (title, copy, action string, enabled bool) {
	running, _ := v.services.snapshot()
	if !running {
		if v.cfg.Role == config.RoleController && len(v.cfg.Peers) == 0 {
			return "添加你的另一台电脑", "两端核对配对码后建立连接，不会自动捕获键鼠。", "添加电脑", true
		}
		return "共享连接尚未启动", "键鼠留在本机，文字与文件尚未同步。", "建立连接", true
	}
	if v.view.Paused {
		return "全部共享已暂停", "键鼠留在本机，不读取或发送新的共享内容。", "恢复共享", true
	}
	if v.view.Target != "" {
		name := peerName(v.view.Target, v.view.Peers, v.records)
		if v.cfg.Role == config.RoleController {
			return "正在控制 " + name, "按 " + emergencyShortcut() + " 随时结束控制。", "停止控制", true
		}
		return "正在接受 " + name + " 的控制", "你可以随时暂停接受控制。", "暂停接受控制", true
	}
	if connectionCount(v.view) == 0 {
		if v.view.Phase == "listening" {
			return "等待主电脑连接", "连接只接受已配对的电脑。添加新电脑时，请打开配对。", "允许配对", true
		}
		return phaseLabel(v.view.Phase), "键鼠保留在本机。连接成功后可开始控制。", "正在连接", false
	}
	if v.cfg.Role == config.RoleAgent {
		if !v.view.Accepting {
			return "已暂停接受控制", "文字与文件共享仍按你的设置运行。", "允许接受控制", true
		}
		return "等待主电脑开始控制", "连接已就绪，主电脑需要主动开始控制。", "暂停接受控制", true
	}
	for _, permission := range v.permissions {
		if permission.State == "denied" || permission.State == "unavailable" {
			return "还差一项系统权限", "文字和文件可独立共享；开始键鼠控制需要系统授权。", "查看权限", true
		}
	}
	for _, peer := range v.view.Peers {
		if peer.Status == "connected" && peer.Control {
			return "正在操作本机", "已连接 " + peerName(peer.ID, v.view.Peers, v.records) + "。开始控制前，键鼠始终留在本机。", "开始控制", !v.layoutEditing
		}
	}
	return "连接已建立", "对端尚未声明安全控制能力。请两端均使用新版桌面界面，并将另一台设为子电脑。", "等待对端就绪", false
}
func (v *desktopView) performMainAction() {
	_, _, action, _ := v.controlSummary()
	switch action {
	case "添加电脑", "允许配对":
		openPairing(v.application, v.options, v.pairings)
	case "建立连接":
		v.wantConnect = true
		v.autoAttempted = true
		cfg := v.cfg
		v.perform("正在建立共享连接…", func(context.Context) error { return v.services.start(v.options, cfg) }, nil)
	case "查看权限":
		v.navigate("permissions")
	case "恢复共享":
		v.send(desktop.Command{Action: "pause", Enabled: false}, "正在恢复共享…")
	case "停止控制":
		v.send(desktop.Command{Action: "stop_control"}, "正在结束控制并释放按键…")
	case "暂停接受控制":
		v.send(desktop.Command{Action: "accept", Enabled: false}, "正在停止接受键鼠控制…")
	case "允许接受控制":
		v.send(desktop.Command{Action: "accept", Enabled: true}, "正在恢复接受控制…")
	case "开始控制":
		for _, peer := range v.view.Peers {
			if peer.Status == "connected" && peer.Control {
				v.send(desktop.Command{Action: "start_control", Target: peer.ID}, "正在确认对端权限并启动控制…")
				break
			}
		}
	}
}

func (v *desktopView) showConnectionActions() {
	cfg := v.cfg
	running, _ := v.services.snapshot()
	box := container.NewVBox()
	var popup dialog.Dialog
	if running {
		text := "暂停全部共享"
		if v.view.Paused {
			text = "恢复全部共享"
		}
		box.Add(widget.NewButton(text, func() {
			popup.Hide()
			v.send(desktop.Command{Action: "pause", Enabled: !v.view.Paused}, "正在应用共享状态…")
		}))
		box.Add(widget.NewButton("断开连接（保留信任）", func() {
			popup.Hide()
			v.wantConnect = false
			v.perform("正在安全断开…", v.services.stopGracefully, nil)
		}))
		box.Add(widget.NewButton("重新连接", func() {
			popup.Hide()
			v.perform("正在安全重新连接…", func(ctx context.Context) error {
				if err := v.services.stopGracefully(ctx); err != nil {
					return err
				}
				return v.services.start(v.options, cfg)
			}, nil)
		}))
	}
	box.Add(widget.NewButton("权限与安全", func() { popup.Hide(); v.navigate("permissions") }))
	box.Add(widget.NewButton("设置", func() { popup.Hide(); v.openSettings() }))
	popup = dialog.NewCustom("连接与共享", "关闭", box, v.window)
	popup.Show()
}

func (v *desktopView) transfersPage() fyne.CanvasObject {
	content := container.NewVBox(pageHeader("传输", "完成表示接收端已校验、提交并发布到剪贴板。"))
	if len(v.view.Tasks) == 0 {
		content.Add(surface(container.NewVBox(sectionTitle("暂无文件传输"), paragraph("在已连接电脑上复制文件或文件夹后，任务会显示在这里。文字同步不会生成文件任务。"))))
		return content
	}
	for _, task := range v.view.Tasks {
		task := task
		name := task.Name
		if name == "" {
			name = "文件任务"
		}
		direction := "本机 → " + peerName(task.PeerID, v.view.Peers, v.records)
		if task.Direction == "receive" {
			direction = peerName(task.PeerID, v.view.Peers, v.records) + " → 本机"
		}
		status := widget.NewLabel(taskLabel(task.State))
		if task.State == "failed" {
			status.Importance = widget.DangerImportance
		} else if task.State == "completed" {
			status.Importance = widget.SuccessImportance
		}
		box := container.NewVBox(container.NewBorder(nil, nil, nil, status, container.NewVBox(sectionTitle(name), secondary(direction))))
		if task.Total > 0 {
			progress := widget.NewProgressBar()
			progress.Max = float64(task.Total)
			progress.SetValue(float64(task.Bytes))
			box.Add(progress)
			caption := "接收端已写入 "
			if task.Direction == "receive" {
				caption = "已接收 "
			}
			box.Add(secondary(caption + formatBytes(task.Bytes) + " / " + formatBytes(task.Total)))
		} else if task.Active() {
			box.Add(widget.NewProgressBarInfinite())
		}
		if task.Error != "" {
			box.Add(paragraph(task.Error))
		}
		actions := container.NewHBox()
		if task.Active() && task.State != "cancelling" && task.State != "unconfirmed" {
			cancel := widget.NewButton("取消传输", func() {
				dialog.ShowConfirm("取消这次传输？", "未完成的内容不会发布到剪贴板。下次需要重新发送。", func(ok bool) {
					if ok {
						v.send(desktop.Command{Action: "cancel_task", Target: task.ID}, "正在请求取消，等待对端确认…")
					}
				}, v.window)
			})
			if v.busy {
				cancel.Disable()
			}
			actions.Add(cancel)
		}
		if task.Direction == "send" && (task.State == "failed" || task.State == "cancelled") {
			retry := primary("重新发送", func() {
				v.send(desktop.Command{Action: "retry_task", Target: task.ID}, "正在建立新的文件任务…")
			})
			if v.busy {
				retry.Disable()
			}
			actions.Add(retry)
		}
		if task.Direction == "receive" && task.State == "completed" && task.Path != "" {
			actions.Add(quiet("在文件夹中显示", func() { v.showError(openFolder(task.Path)) }))
			box.Add(secondary("文件已复制到本机剪贴板，请在需要的位置粘贴。"))
		}
		box.Add(actions)
		content.Add(surface(box))
	}
	return content
}

func (v *desktopView) permissionsPage() fyne.CanvasObject {
	recheck := widget.NewButton("重新检测", func() {
		v.permissionTime = time.Time{}
		v.refresh(true)
		v.notice.SetText("已重新检测本机能力；未授权时，请在系统设置中允许 MyApp 后再检测。")
	})
	content := container.NewVBox(pageHeader("权限与安全", "只开放当前用途需要的能力。", recheck), sectionTitle("系统权限"))
	for _, permission := range v.permissions {
		permission := permission
		row := container.NewVBox(container.NewBorder(nil, nil, nil, widget.NewLabel(permissionLabel(permission.State)), sectionTitle(permission.Name)), secondary(permission.Description))
		if permission.State == "denied" || permission.State == "unknown" {
			row.Add(widget.NewButton("打开系统设置", func() { v.showError(openPermissionSettings(permission.Page)) }))
			row.Add(secondary("若页面名称不同，请查找输入监控、辅助功能或设备控制和数据访问；授权后返回此处重新检测。"))
		}
		content.Add(surface(row))
	}
	content.Add(sectionTitle("信任的电脑"))
	if len(v.records) == 0 {
		content.Add(paragraph("尚未配对其他电脑。未配对的设备不能控制键鼠或访问共享内容。"))
	}
	for _, record := range v.records {
		record := record
		name := peerName(record.DeviceID, v.view.Peers, v.records)
		content.Add(surface(container.NewBorder(nil, nil, nil, container.NewHBox(quiet("详情", func() { v.showPeerDetails(record.DeviceID) }), quiet("撤销信任", func() { v.revokePeer(record.DeviceID) })), container.NewVBox(sectionTitle(name), secondary("已通过双方配对码确认")))))
	}
	content.Add(sectionTitle("连接与隐私"))
	content.Add(paragraph("设备之间使用双向认证和加密通信。MyApp 不提供云中继，也不会将剪贴板正文或文件内容写入日志。"))
	details := container.NewVBox(secondary("监听地址："+v.cfg.ListenAddress), secondary("本机设备 ID："+v.state.DeviceID), secondary("日志："+logging.DefaultPath()), widget.NewButton("导出日志", func() {
		v.exportLogDialog(v.notice.SetText)
	}))
	content.Add(widget.NewAccordion(widget.NewAccordionItem("高级连接与诊断信息", details)))
	return content
}

func (v *desktopView) showPeerDetails(id string) {
	name := peerName(id, v.view.Peers, v.records)
	status := "离线"
	address := "尚未配置连接地址"
	system := "对端未提供"
	version := "对端未提供"
	for _, peer := range v.cfg.Peers {
		if peer.ID == id {
			address = peer.Address
		}
	}
	for _, peer := range v.view.Peers {
		if peer.ID == id {
			if peer.Version != "" {
				version = peer.Version
			}
			if peer.Status == "connected" {
				status = "已连接"
			}
			if peer.Address != "" {
				address = peer.Address
			}
			switch peer.OS {
			case "darwin":
				system = "macOS"
			case "windows":
				system = "Windows"
			case "linux":
				system = "Linux"
			}
		}
	}
	fingerprint := ""
	for _, record := range v.records {
		if record.DeviceID == id {
			fingerprint = record.Fingerprint
		}
	}
	box := container.NewVBox(sectionTitle(name), paragraph("连接状态："+status), paragraph("系统："+system), paragraph("软件版本："+version), paragraph("连接地址："+address), widget.NewAccordion(widget.NewAccordionItem("设备身份", container.NewVBox(paragraph("设备 ID："+id), paragraph("证书指纹："+fingerprint)))))
	var popup dialog.Dialog
	buttons := container.NewHBox(widget.NewButton("撤销信任", func() { popup.Hide(); v.revokePeer(id) }))
	if v.cfg.Role == config.RoleController {
		for _, peer := range v.view.Peers {
			if peer.ID == id && peer.Status == "connected" && peer.Control {
				buttons.Add(primary("控制这台电脑", func() {
					popup.Hide()
					v.send(desktop.Command{Action: "start_control", Target: id}, "正在启动键鼠控制…")
				}))
			}
		}
		buttons.Add(widget.NewButton("修改连接地址", func() { popup.Hide(); v.editPeerAddress(id, address) }))
	}
	box.Add(buttons)
	popup = dialog.NewCustom("设备详情", "完成", box, v.window)
	popup.Resize(fyne.NewSize(540, 360))
	popup.Show()
}

func (v *desktopView) editPeerAddress(id, current string) {
	entry := widget.NewEntry()
	entry.SetText(current)
	form := dialog.NewForm("修改连接地址", "应用并重新连接", "取消", []*widget.FormItem{widget.NewFormItem("主机与服务端口", entry)}, func(ok bool) {
		if !ok {
			return
		}
		host, port, err := net.SplitHostPort(strings.TrimSpace(entry.Text))
		if err != nil || host == "" || port == "" || port == "0" {
			v.showError(errors.New("请输入有效的主机:端口地址"))
			return
		}
		address := strings.TrimSpace(entry.Text)
		v.perform("正在应用连接地址…", func(ctx context.Context) error {
			running, _ := v.services.snapshot()
			if running {
				if err := v.services.stopGracefully(ctx); err != nil {
					return err
				}
			}
			configMu.Lock()
			cfg, err := config.Load(v.options.ConfigPath)
			if err == nil {
				found := false
				for i := range cfg.Peers {
					if cfg.Peers[i].ID == id {
						cfg.Peers[i].Address = address
						found = true
					}
				}
				if !found {
					cfg.Peers = append(cfg.Peers, config.Peer{ID: id, Address: address})
				}
				err = config.Save(v.options.ConfigPath, cfg)
			}
			configMu.Unlock()
			if err != nil {
				return err
			}
			if running {
				return v.services.start(v.options, cfg)
			}
			return nil
		}, func() { v.autoAttempted = false })
	}, v.window)
	form.Resize(fyne.NewSize(520, 200))
	form.Show()
}

func (v *desktopView) changeSharing(kind string, enabled bool) {
	old := v.cfg.Clipboard.TextEnabled
	if kind == "files" {
		old = v.cfg.Files.Enabled
	}
	v.perform("正在应用共享设置…", func(ctx context.Context) error {
		running, _ := v.services.snapshot()
		if running {
			if err := v.services.command(ctx, desktop.Command{Action: kind, Enabled: enabled}); err != nil {
				return err
			}
		}
		configMu.Lock()
		cfg, err := config.Load(v.options.ConfigPath)
		if err == nil {
			if kind == "text" {
				cfg.Clipboard.TextEnabled = enabled
			} else {
				cfg.Files.Enabled = enabled
			}
			err = config.Save(v.options.ConfigPath, cfg)
		}
		configMu.Unlock()
		if err != nil && running {
			_ = v.services.command(ctx, desktop.Command{Action: kind, Enabled: old})
		}
		return err
	}, nil)
}

func (v *desktopView) toggleLayout() {
	if !v.layoutEditing {
		begin := func() {
			v.layoutDraft = make(map[string]string)
			for i, peer := range v.cfg.Peers {
				position := peer.Position
				if position == "" {
					position = "right"
					if i == 1 {
						position = "left"
					}
				}
				v.layoutDraft[peer.ID] = position
			}
			v.layoutEditing = true
			v.refresh(true)
		}
		if v.view.Target != "" {
			v.perform("正在结束控制后编辑位置…", func(ctx context.Context) error {
				return v.services.command(ctx, desktop.Command{Action: "stop_control"})
			}, begin)
		} else {
			begin()
		}
		return
	}
	seen := make(map[string]bool)
	for _, position := range v.layoutDraft {
		if seen[position] {
			v.showError(errors.New("两台电脑不能放在同一个位置，请选择不同方向"))
			return
		}
		seen[position] = true
	}
	configMu.Lock()
	cfg, err := config.Load(v.options.ConfigPath)
	if err == nil {
		for i := range cfg.Peers {
			cfg.Peers[i].Position = v.layoutDraft[cfg.Peers[i].ID]
		}
		err = config.Save(v.options.ConfigPath, cfg)
	}
	configMu.Unlock()
	if err != nil {
		v.showError(err)
		return
	}
	v.layoutEditing = false
	v.notice.SetText("电脑位置已保存；当前仍使用快捷键切换目标。")
	v.refresh(true)
}
func positionLabel(value string) string {
	switch value {
	case "left":
		return "左侧"
	case "up":
		return "上方"
	case "down":
		return "下方"
	default:
		return "右侧"
	}
}
func positionValue(value string) string {
	switch value {
	case "左侧":
		return "left"
	case "上方":
		return "up"
	case "下方":
		return "down"
	default:
		return "right"
	}
}
