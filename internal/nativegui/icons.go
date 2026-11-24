//go:build gui

package nativegui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed assets/myapp-windows.png
var windowsIcon []byte

func applicationIcon(goos string) fyne.Resource {
	if goos == "windows" {
		return fyne.NewStaticResource("myapp-windows.png", windowsIcon)
	}
	return fyne.NewStaticResource("myapp-app.svg", appIcon)
}

func systemTrayIcon(goos string) fyne.Resource {
	if goos == "windows" {
		return applicationIcon(goos)
	}
	return fyne.NewStaticResource("myapp-tray.svg", trayIcon)
}
