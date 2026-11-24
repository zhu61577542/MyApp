//go:build gui

package nativegui

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	fynedesktop "fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/instance"
	"myapp/internal/trust"
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
	Background        bool
	Version           string
	Commit            string
}

type desktopView struct {
	application      fyne.App
	window           fyne.Window
	options          Options
	notified         desktop.NotificationGate
	reconnectSince   time.Time
	services         *serviceManager
	pairings         *pairingManager
	prefs            preferences
	root             *fyne.Container
	main             *fyne.Container
	nav              *widget.List
	settingsButton   *widget.Button
	notice           *widget.Label
	footer           *widget.Label
	page             string
	ready            bool
	state            state
	cfg              config.Config
	view             desktop.Snapshot
	records          []trust.Record
	permissions      []permission
	permissionTime   time.Time
	signature        string
	wantConnect      bool
	autoAttempted    bool
	nextAutoConnect  time.Time
	servicePeers     string
	busy             bool
	closing          bool
	setupStep        int
	setupName        string
	setupRole        config.Role
	layoutEditing    bool
	layoutResumeEdge bool
	layoutDraft      map[string]string
	settingsWindow   fyne.Window
	tray             *fyne.Menu
}

func Run(ctx context.Context, options Options) error {
	guard, err := instance.Acquire("desktop-gui")
	if errors.Is(err, instance.ErrRunning) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("无法建立主程序单实例保护: %w", err)
	}
	defer guard.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	application := app.NewWithID("local.myapp.client")
	application.SetIcon(applicationIcon(runtime.GOOS))
	window := application.NewWindow("MyApp")
	window.Resize(fyne.NewSize(1000, 700))
	services := &serviceManager{status: "已停止"}
	pairings := &pairingManager{}
	view := newDesktopView(application, window, options, services, pairings)
	window.SetContent(view.root)
	window.SetCloseIntercept(func() {
		window.Hide()
		if !view.prefs.CloseHintShown {
			view.prefs.CloseHintShown = true
			_ = savePreferences(options, view.prefs)
			location := "系统托盘"
			if runtime.GOOS == "darwin" {
				location = "菜单栏"
			}
			application.SendNotification(fyne.NewNotification("MyApp 仍在后台运行", "从"+location+"图标重新打开。要停止全部共享，请选择“退出 MyApp”。"))
		}
	})
	view.installMenus()
	application.Settings().AddListener(func(fyne.Settings) { fyne.Do(func() { view.refresh(false) }) })
	view.refresh(true)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				services.stop()
				pairings.stop()
				fyne.Do(application.Quit)
				return
			case <-guard.Activations:
				fyne.Do(func() { window.Show(); window.RequestFocus() })
			case <-ticker.C:
				fyne.Do(func() {
					if !view.closing {
						view.refresh(false)
					}
				})
			}
		}
	}()
	if !options.Background || !view.ready {
		window.Show()
		fyne.Do(func() { view.requestPermissions(true) })
	}
	application.Run()
	services.stop()
	pairings.stop()
	return nil
}

func newDesktopView(application fyne.App, window fyne.Window, options Options, services *serviceManager, pairings *pairingManager) *desktopView {
	prefs, err := loadPreferences(options)
	if err != nil {
		prefs = preferences{Appearance: "system", ConnectAutomatically: true}
	}
	view := &desktopView{application: application, window: window, options: options, services: services, pairings: pairings, prefs: prefs, page: "devices", wantConnect: true, setupStep: 1, setupName: options.DefaultDeviceName, layoutDraft: make(map[string]string)}
	if current, _ := loadState(options); current.Ready {
		if _, err := os.Stat(preferencesPath(options)); errors.Is(err, os.ErrNotExist) {
			view.prefs.OnboardingDone = true
			_ = savePreferences(options, view.prefs)
		}
	}
	application.Settings().SetTheme(desktopTheme{appearance: view.prefs.Appearance})
	view.main = container.NewStack()
	view.notice = paragraph("")
	view.notice.Importance = widget.LowImportance
	view.footer = secondary("")
	view.root = container.NewStack()
	return view
}

func buildContent(application fyne.App, options Options, services *serviceManager, pairings *pairingManager) (fyne.CanvasObject, func()) {
	window := application.NewWindow("MyApp")
	view := newDesktopView(application, window, options, services, pairings)
	view.refresh(true)
	return view.root, func() { view.refresh(false) }
}

func (v *desktopView) refresh(force bool) {
	current, err := loadState(v.options)
	if err != nil {
		v.showError(err)
		return
	}
	v.state = current
	v.ready = current.Ready
	if current.Ready {
		if cfg, err := config.Load(v.options.ConfigPath); err == nil {
			if len(cfg.Peers) > len(v.cfg.Peers) {
				v.autoAttempted = false
			}
			v.cfg = cfg
		}
		records, err := trust.New(v.options.TrustDirectory).List()
		if err == nil {
			if len(records) > len(v.records) {
				v.wantConnect = true
				v.autoAttempted = false
			}
			v.records = records
			if len(records) > 0 && !v.prefs.OnboardingDone {
				v.prefs.OnboardingDone = true
				if e := savePreferences(v.options, v.prefs); e != nil {
					v.notice.SetText(e.Error())
				}
			}
		} else {
			v.notice.SetText("无法读取信任列表：" + err.Error())
		}
	}
	view, serviceError := v.services.snapshotState()
	v.view = view
	running, status := v.services.snapshot()
	peerKey := string(v.cfg.Role)
	for _, peer := range v.cfg.Peers {
		peerKey += "\x00" + peer.ID + "\x00" + peer.Address
	}
	if running && v.servicePeers == "" {
		v.servicePeers = peerKey
	}
	if running && v.wantConnect && !view.Paused && !v.busy && !v.layoutEditing && !v.pairings.snapshot().Running && v.servicePeers != peerKey && !time.Now().Before(v.nextAutoConnect) {
		cfg := v.cfg
		v.nextAutoConnect = time.Now().Add(3 * time.Second)
		v.perform("正在自动连接新信任的电脑…", func(ctx context.Context) error {
			if err := v.services.stopGracefully(ctx); err != nil {
				return err
			}
			return v.services.start(v.options, cfg)
		}, func() { v.servicePeers = peerKey })
		return
	}
	if time.Since(v.permissionTime) > 3*time.Second || force {
		v.permissions = checkPermissions(v.cfg.Role)
		v.permissionTime = time.Now()
	}
	v.notifyProblems(running)
	if v.ready && v.prefs.OnboardingDone && v.wantConnect && !running && !v.busy && !v.layoutEditing && !v.pairings.snapshot().Running && !time.Now().Before(v.nextAutoConnect) {
		v.autoAttempted = true
		v.nextAutoConnect = time.Now().Add(3 * time.Second)
		if v.cfg.Role == config.RoleAgent || len(v.cfg.Peers) > 0 {
			if err := v.services.start(v.options, v.cfg); err != nil {
				v.notice.SetText(err.Error())
			} else {
				v.servicePeers = peerKey
			}
		}
	}
	stat, _ := os.Stat(v.options.ConfigPath)
	stamp := ""
	if stat != nil {
		stamp = stat.ModTime().String()
	}
	pair := v.pairings.snapshot()
	key := fmt.Sprintf("%t/%d/%s/%s/%s/%t/%s/%t/%s/%v/%v", current.Ready, view.Revision, view.Phase, serviceError, stamp, running, status, pair.Running, pair.Status, v.application.Settings().ThemeVariant(), v.permissions)
	if !force && key == v.signature {
		return
	}
	v.signature = key
	if !force && (v.page == "settings" || v.layoutEditing) {
		v.refreshTray()
		return
	}
	if !v.ready || !v.prefs.OnboardingDone {
		v.renderSetup()
		v.refreshTray()
		return
	}
	v.renderShell()
	v.refreshTray()
}

func (v *desktopView) renderShell() {
	names := []string{"设备", "传输", "权限与安全"}
	pages := []string{"devices", "transfers", "permissions"}
	icons := []fyne.Resource{theme.ComputerIcon(), theme.FolderIcon(), theme.ConfirmIcon()}
	v.nav = widget.NewList(func() int { return len(names) }, func() fyne.CanvasObject {
		return container.NewHBox(widget.NewIcon(theme.ComputerIcon()), widget.NewLabel("权限与安全"))
	}, func(id widget.ListItemID, item fyne.CanvasObject) {
		box := item.(*fyne.Container)
		box.Objects[0].(*widget.Icon).SetResource(icons[id])
		box.Objects[1].(*widget.Label).SetText(names[id])
	})
	v.nav.OnSelected = func(id widget.ListItemID) { v.navigate(pages[id]) }
	for i, page := range pages {
		if page == v.page {
			v.nav.OnSelected = nil
			v.nav.Select(i)
			v.nav.OnSelected = func(id widget.ListItemID) { v.navigate(pages[id]) }
		}
	}
	brand := container.NewHBox(widget.NewIcon(fyne.NewStaticResource("myapp-app.svg", appIcon)), sectionTitle("MyApp"))
	v.settingsButton = quiet("设置", v.openSettings)
	if v.page == "settings" {
		v.settingsButton.Importance = widget.HighImportance
	}
	bottom := container.NewVBox(v.settingsButton, widget.NewSeparator(), secondary(fmt.Sprintf("已连接 %d 台电脑", connectionCount(v.view))), secondary("仅局域网 · 双向加密"))
	sidebar := container.NewBorder(container.NewPadded(brand), bottom, nil, nil, v.nav)
	sidebar = container.New(&minimumLayout{minimum: fyne.NewSize(172, 0)}, sidebar)
	var page fyne.CanvasObject
	switch v.page {
	case "transfers":
		page = v.transfersPage()
	case "permissions":
		page = v.permissionsPage()
	case "settings":
		page = v.settingsPage()
	default:
		page = v.devicesPage()
	}
	v.main.Objects = []fyne.CanvasObject{page}
	v.main.Refresh()
	v.footer.SetText(roleName(v.cfg.Role) + " · " + v.cfg.DeviceName + "     结束控制：" + emergencyShortcut())
	center := container.NewBorder(nil, container.NewVBox(v.notice, v.footer), nil, nil, container.NewVScroll(v.main))
	content := container.NewBorder(nil, nil, container.NewPadded(surface(sidebar)), nil, container.NewPadded(center))
	v.root.Objects = []fyne.CanvasObject{container.New(&minimumLayout{minimum: fyne.NewSize(760, 560)}, content)}
	v.root.Refresh()
}

type minimumLayout struct{ minimum fyne.Size }

func (l *minimumLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		object.Move(fyne.NewPos(0, 0))
		object.Resize(size)
	}
}
func (l *minimumLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := l.minimum
	for _, object := range objects {
		size = size.Max(object.MinSize())
	}
	return size
}

func (v *desktopView) navigate(page string) {
	if v.layoutEditing {
		v.notice.SetText("请先保存或取消电脑位置调整")
		return
	}
	v.page = page
	v.refresh(true)
}
func (v *desktopView) showError(err error) {
	if err != nil {
		v.notice.SetText(err.Error())
	}
}
func (v *desktopView) perform(message string, action func(context.Context) error, success func(), failure ...func(error)) {
	if v.busy {
		return
	}
	v.busy = true
	v.notice.SetText(message)
	v.refresh(true)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		err := action(ctx)
		fyne.Do(func() {
			v.busy = false
			if err != nil {
				v.showError(err)
				if len(failure) > 0 {
					failure[0](err)
				}
			} else {
				v.notice.SetText("操作已应用")
				if success != nil {
					success()
				}
			}
			v.refresh(true)
		})
	}()
}
func (v *desktopView) send(command desktop.Command, message string) {
	v.perform(message, func(ctx context.Context) error { return v.services.command(ctx, command) }, nil)
}

func (v *desktopView) installMenus() {
	settings := fyne.NewMenuItem("设置…", v.openSettings)
	modifier := fyne.KeyModifierControl
	if runtime.GOOS == "darwin" {
		modifier = fyne.KeyModifierSuper
	}
	shortcut := &fynedesktop.CustomShortcut{KeyName: fyne.KeyComma, Modifier: modifier}
	settings.Shortcut = shortcut
	v.window.Canvas().AddShortcut(shortcut, func(fyne.Shortcut) { v.openSettings() })
	quit := fyne.NewMenuItem("退出 MyApp", v.requestQuit)
	quit.IsQuit = true
	if runtime.GOOS == "darwin" {
		v.window.SetMainMenu(fyne.NewMainMenu(fyne.NewMenu("MyApp", settings, fyne.NewMenuItemSeparator(), quit)))
	}
	if desktopApp, ok := v.application.(fynedesktop.App); ok {
		desktopApp.SetSystemTrayIcon(systemTrayIcon(runtime.GOOS))
	}
	v.refreshTray()
}

func (v *desktopView) refreshTray() {
	app, ok := v.application.(fynedesktop.App)
	if !ok {
		return
	}
	status := fmt.Sprintf("已连接 %d 台电脑", connectionCount(v.view))
	if v.view.Target != "" {
		status = "控制目标：" + peerName(v.view.Target, v.view.Peers, v.records)
	}
	if v.view.Paused {
		status = "全部共享已暂停"
	}
	summary := fyne.NewMenuItem(status, nil)
	summary.Disabled = true
	pauseText := "暂停全部共享"
	if v.view.Paused {
		pauseText = "恢复全部共享"
	}
	pause := fyne.NewMenuItem(pauseText, func() {
		v.send(desktop.Command{Action: "pause", Enabled: !v.view.Paused}, "正在应用共享状态…")
	})
	running, _ := v.services.snapshot()
	pause.Disabled = !running || v.busy
	quit := fyne.NewMenuItem("退出 MyApp", v.requestQuit)
	quit.IsQuit = true
	menu := fyne.NewMenu("MyApp", summary, fyne.NewMenuItem("打开 MyApp", func() { v.window.Show(); v.window.RequestFocus() }), pause, fyne.NewMenuItem("传输任务", func() { v.window.Show(); v.navigate("transfers") }), fyne.NewMenuItem("权限与安全", func() { v.window.Show(); v.navigate("permissions") }), fyne.NewMenuItem("设置…", v.openSettings), fyne.NewMenuItemSeparator(), quit)
	pair := v.pairings.snapshot()
	if pair.Running {
		menu.Items = append([]*fyne.MenuItem{fyne.NewMenuItem("配对正在后台进行", func() { openPairing(v.application, v.options, v.pairings) })}, menu.Items...)
	}
	v.tray = menu
	app.SetSystemTrayMenu(menu)
}

func (v *desktopView) requestQuit() {
	active := false
	for _, task := range v.view.Tasks {
		if task.Active() {
			active = true
		}
	}
	stop := func() {
		if v.closing {
			return
		}
		finish := func() { v.closing = true; v.pairings.stop(); v.application.Quit() }
		v.perform("正在停止共享并释放键鼠…", func(ctx context.Context) error { return v.services.stopGracefully(ctx) }, finish, func(err error) {
			confirm := dialog.NewConfirm("服务仍在停止", err.Error()+"。强制结束可能无法确认对端的最后一次释放；对端会按断线保护恢复。", func(force bool) {
				if force {
					v.services.forceStop()
					v.perform("正在结束服务…", v.services.waitStopped, finish)
				}
			}, v.window)
			confirm.SetConfirmText("强制结束并退出")
			confirm.SetDismissText("继续等待")
			confirm.Show()
		})
	}
	if active {
		dialog.ShowConfirm("有文件任务正在进行", "退出会停止共享并取消未完成的任务。你也可以暂不退出，等待任务完成。", func(confirmed bool) {
			if confirmed {
				stop()
			}
		}, v.window)
		return
	}
	stop()
}

func removePeer(options Options, peerID string) error {
	configMu.Lock()
	defer configMu.Unlock()
	store := trust.New(options.TrustDirectory)
	if err := store.Revoke(peerID, time.Now()); err != nil && !errors.Is(err, trust.ErrRevoked) {
		return err
	}
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
	cfg.Peers = filtered
	return config.Save(options.ConfigPath, cfg)
}

func (v *desktopView) revokePeer(id string) {
	name := peerName(id, v.view.Peers, v.records)
	dialog.ShowConfirm("撤销对“"+name+"”的信任？", "控制、共享连接及未完成的配对将停止。再次连接需在两端重新核对配对码。已保存的文件不会被删除。", func(confirmed bool) {
		if !confirmed {
			return
		}
		v.perform("正在停止连接并撤销信任…", func(ctx context.Context) error {
			if err := v.pairings.stopAndWait(ctx); err != nil {
				return err
			}
			if err := v.services.stopGracefully(ctx); err != nil {
				return err
			}
			return removePeer(v.options, id)
		}, func() { v.autoAttempted = false; v.signature = "" })
	}, v.window)
}

func (v *desktopView) openSettings() {
	if runtime.GOOS != "darwin" {
		v.page = "settings"
		v.refresh(true)
		return
	}
	if v.settingsWindow != nil {
		v.settingsWindow.Show()
		v.settingsWindow.RequestFocus()
		return
	}
	window := v.application.NewWindow("MyApp 设置")
	v.settingsWindow = window
	window.Resize(fyne.NewSize(610, 680))
	window.SetContent(container.NewPadded(container.NewVScroll(v.settingsPage())))
	window.SetOnClosed(func() { v.settingsWindow = nil })
	window.Show()
}

func shortError(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > 0 {
		return lines[0]
	}
	return text
}
