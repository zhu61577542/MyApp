//go:build gui

package nativegui

import (
	"fyne.io/fyne/v2"
	"time"
)

func (v *desktopView) notifyProblems(running bool) {
	previous := v.notified
	v.notified = make(map[string]bool)
	notify := func(key, title, body string) {
		v.notified[key] = true
		if !previous[key] {
			v.application.SendNotification(fyne.NewNotification(title, body))
		}
	}
	for _, task := range v.view.Tasks {
		if task.State == "failed" {
			notify("task:"+task.ID, "MyApp 文件任务未完成", "从菜单栏或托盘打开“传输任务”，查看原因并重新发送。")
		}
	}
	if running && v.wantConnect && !v.view.Paused && (v.view.Phase == "reconnecting" || v.view.Phase == "connecting") {
		if v.reconnectSince.IsZero() {
			v.reconnectSince = time.Now()
		}
		if time.Since(v.reconnectSince) >= 15*time.Second {
			notify("connection", "MyApp 暂时无法连接", "键鼠保留在本机。打开设备页，检查另一台电脑、连接地址与网络。")
		}
	} else {
		v.reconnectSince = time.Time{}
	}
	if running && connectionCount(v.view) > 0 && !v.view.Paused {
		for _, permission := range v.permissions {
			if permission.State == "denied" {
				notify("permission:"+permission.Name, "MyApp 需要系统授权", "从菜单栏或托盘打开“权限与安全”，完成授权后重新检测。")
			}
		}
	}
}
