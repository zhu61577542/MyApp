//go:build gui

package nativegui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/config"
	"myapp/internal/identity"
)

func initializeDevice(options Options, name string, role config.Role) error {
	name = strings.TrimSpace(name)
	cfg := config.Default(name, role)
	if err := cfg.Validate(); err != nil {
		return err
	}
	configMu.Lock()
	defer configMu.Unlock()
	if _, err := identity.Load(options.IdentityDirectory); errors.Is(err, os.ErrNotExist) {
		local, err := identity.Generate(name, time.Now())
		if err != nil {
			return err
		}
		if err := identity.SaveNew(options.IdentityDirectory, local); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if old, err := config.Load(options.ConfigPath); err == nil {
		old.DeviceName = name
		old.Role = role
		if role == config.RoleAgent {
			old.Peers = nil
		}
		return config.Save(options.ConfigPath, old)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return config.SaveNew(options.ConfigPath, cfg)
}

func (v *desktopView) renderSetup() {
	steps := secondary("1 命名与用途    ·    2 系统权限    ·    3 添加电脑")
	body := container.NewVBox(steps)
	if v.setupStep == 1 {
		body.Add(heading("让你的电脑协同工作"))
		body.Add(paragraph(v.state.SetupReason))
		name := widget.NewEntry()
		name.SetText(v.setupName)
		name.SetPlaceHolder("这台电脑的名称")
		choices := []string{"主电脑 · 用本机键鼠控制其他电脑", "子电脑 · 接受主电脑控制"}
		role := widget.NewRadioGroup(choices, nil)
		role.Required = true
		if v.setupRole == config.RoleController {
			role.SetSelected(choices[0])
		} else if v.setupRole == config.RoleAgent {
			role.SetSelected(choices[1])
		}
		role.OnChanged = func(value string) {
			if value == choices[0] {
				v.setupRole = config.RoleController
			} else {
				v.setupRole = config.RoleAgent
			}
		}
		name.OnChanged = func(value string) { v.setupName = value }
		next := primary("继续", func() {
			if v.setupRole == "" {
				v.showError(errors.New("请选择这台电脑的用途"))
				return
			}
			name, role, prefs := v.setupName, v.setupRole, v.prefs
			v.perform("正在保存本机设置…", func(context.Context) error {
				if err := initializeDevice(v.options, name, role); err != nil {
					return err
				}
				return savePreferences(v.options, prefs)
			}, func() { v.setupStep = 2; v.refresh(true) })
		})
		if v.busy {
			next.Disable()
		}
		body.Add(widget.NewForm(widget.NewFormItem("设备名称", name)))
		body.Add(sectionTitle("这台电脑如何使用"))
		body.Add(role)
		body.Add(next)
		if !strings.Contains(v.state.SetupReason, "欢迎") && !strings.Contains(v.state.SetupReason, "补充") && !strings.Contains(v.state.SetupReason, "建立") && v.state.SetupReason != "" {
			body.Add(widget.NewButton("恢复本机设置", v.recoverIdentity))
		}
	} else if v.setupStep == 2 {
		body.Add(heading("允许必要的系统权限"))
		body.Add(paragraph("权限由你决定。未授权时仍可使用独立的文字与文件共享；键鼠控制保持禁用。"))
		for _, permission := range checkPermissions(v.cfg.Role) {
			permission := permission
			row := container.NewVBox(container.NewBorder(nil, nil, nil, widget.NewLabel(permissionLabel(permission.State)), sectionTitle(permission.Name)), secondary(permission.Description))
			if permission.Page != "" {
				row.Add(widget.NewButton("打开系统设置", func() { v.showError(openPermissionSettings(permission.Page)) }))
			}
			body.Add(surface(row))
		}
		body.Add(widget.NewButton("重新检测", func() { v.refresh(true) }))
		body.Add(primary("继续，也可稍后设置权限", func() { v.setupStep = 3; v.refresh(true) }))
	} else {
		body.Add(heading("添加你的另一台电脑"))
		body.Add(paragraph("在另一台电脑打开 MyApp，选择添加电脑并允许被发现。两端分别确认同一配对码后才建立信任。"))
		body.Add(primary("查找并添加电脑", func() { openPairing(v.application, v.options, v.pairings) }))
		body.Add(widget.NewButton("先进入主窗口", func() {
			v.prefs.OnboardingDone = true
			if err := savePreferences(v.options, v.prefs); err != nil {
				v.prefs.OnboardingDone = false
				v.showError(err)
				return
			}
			v.autoAttempted = false
			v.refresh(true)
		}))
	}
	body.Add(v.notice)
	v.root.Objects = []fyne.CanvasObject{container.NewPadded(container.NewVScroll(container.New(&setupLayout{}, body)))}
	v.root.Refresh()
}

type setupLayout struct{}

func (*setupLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	size := fyne.NewSize(660, 500)
	for _, object := range objects {
		size = size.Max(object.MinSize().Add(fyne.NewSize(0, 48)))
	}
	return size
}
func (*setupLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, object := range objects {
		width := float32(560)
		if size.Width < width {
			width = size.Width
		}
		object.Resize(fyne.NewSize(width, object.MinSize().Height))
		object.Move(fyne.NewPos((size.Width-width)/2, 24))
	}
}

func (v *desktopView) recoverIdentity() {
	prefs := v.prefs
	prefs.OnboardingDone = false
	dialog.ShowConfirm("重新建立本机身份？", "旧设置将备份到应用数据目录。此电脑需要与其他电脑重新配对，原有身份不会静默覆盖。", func(ok bool) {
		if !ok {
			return
		}
		v.perform("正在备份并恢复设置…", func(ctx context.Context) error {
			if err := v.pairings.stopAndWait(ctx); err != nil {
				return err
			}
			if err := v.services.stopGracefully(ctx); err != nil {
				return err
			}
			configMu.Lock()
			defer configMu.Unlock()
			backup := filepath.Join(filepath.Dir(v.options.ConfigPath), "backup-"+time.Now().Format("20060102-150405"))
			if err := os.MkdirAll(backup, 0700); err != nil {
				return err
			}
			for _, name := range []string{v.options.ConfigPath, v.options.IdentityDirectory} {
				if _, err := os.Stat(name); errors.Is(err, os.ErrNotExist) {
					continue
				} else if err != nil {
					return err
				}
				if err := os.Rename(name, filepath.Join(backup, filepath.Base(name))); err != nil {
					return err
				}
			}
			return savePreferences(v.options, prefs)
		}, func() {
			v.prefs = prefs
			v.setupStep = 1
			v.notice.SetText("旧设置已备份，请重新命名并选择用途。")
		})
	}, v.window)
}
