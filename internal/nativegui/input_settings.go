//go:build gui

package nativegui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"myapp/internal/desktop"
	"myapp/internal/input"
)

func (m *serviceManager) saveInputSettings(ctx context.Context, path string, settings input.Settings) error {
	m.mu.Lock()
	if m.cmd == nil {
		defer m.mu.Unlock()
		return input.SaveSettings(path, settings)
	}
	m.mu.Unlock()
	return m.command(ctx, desktop.Command{Action: "input_settings", Input: &settings})
}

func (v *desktopView) inputSettingsContent() fyne.CanvasObject {
	path := input.SettingsPath(v.options.ConfigPath)
	settings, err := input.LoadSettings(path)
	status := paragraph("单独保存；下次跨屏时生效，无需重新连接。")
	if err != nil {
		settings = input.DefaultSettings()
		status.SetText("速度设置读取失败：" + err.Error() + "。可调整后保存，或恢复默认。")
	}
	motion := widget.NewSlider(10, 400)
	scroll := widget.NewSlider(10, 400)
	motion.Step, scroll.Step = 10, 10
	motionValue, scrollValue := widget.NewLabel(""), widget.NewLabel("")
	motion.OnChanged = func(value float64) { motionValue.SetText(fmt.Sprintf("%.0f%%", value)) }
	scroll.OnChanged = func(value float64) { scrollValue.SetText(fmt.Sprintf("%.0f%%", value)) }
	motion.SetValue(float64(settings.PointerPercent))
	scroll.SetValue(float64(settings.ScrollPercent))
	var apply, reset *widget.Button
	save := func() {
		if v.busy {
			return
		}
		v.busy = true
		apply.Disable()
		reset.Disable()
		motion.Disable()
		scroll.Disable()
		updated := input.Settings{PointerPercent: int(motion.Value), ScrollPercent: int(scroll.Value)}
		status.SetText("正在保存速度设置…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := v.services.saveInputSettings(ctx, path, updated)
			fyne.Do(func() {
				v.busy = false
				apply.Enable()
				reset.Enable()
				motion.Enable()
				scroll.Enable()
				if err != nil {
					status.SetText("未确认保存：" + err.Error() + "；可重新打开设置核对。")
					return
				}
				status.SetText("速度设置已保存，下次跨屏时生效。")
			})
		}()
	}
	apply = widget.NewButton("保存速度", save)
	reset = widget.NewButton("恢复默认并保存", func() {
		if v.busy {
			return
		}
		motion.SetValue(100)
		scroll.SetValue(100)
		save()
	})
	return surface(container.NewVBox(
		paragraph("本机键鼠控制其他电脑时的速度；两端独立调整。"),
		widget.NewForm(
			widget.NewFormItem("移动速度", container.NewBorder(nil, nil, nil, motionValue, motion)),
			widget.NewFormItem("滚动速度", container.NewBorder(nil, nil, nil, scrollValue, scroll))),
		secondary("100% 为标准倍率。移动偏快可先试70%；不同鼠标或触控板的手感需要分别校准。"),
		container.NewHBox(apply, reset), status))
}
