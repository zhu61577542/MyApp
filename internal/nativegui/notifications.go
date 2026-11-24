//go:build gui

package nativegui

import (
	"fyne.io/fyne/v2"
	"time"
)

func (v *desktopView) notifyProblems(running bool) {
	notify := func(key, title, body string) {
		if v.notified.Take(key) {
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
		if running && connectionCount(v.view) > 0 {
			v.notified.Resolved("connection")
		}
	}
	// 权限预检只在页面展示；系统授权由用户操作时请求，避免误报和重复通知。
}
