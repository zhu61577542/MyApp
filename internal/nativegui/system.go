//go:build gui

package nativegui

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func openFolder(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("/usr/bin/open", path).Run()
	case "windows":
		return exec.Command("explorer.exe", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
func openPermissionSettings(page string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("请打开系统网络与防火墙设置，允许 MyApp 访问局域网")
	}
	if page != "Privacy_Accessibility" && page != "Privacy_ListenEvent" {
		return errors.New("未知权限页面")
	}
	address := url.URL{Scheme: "x-apple.systempreferences", Opaque: "com.apple.preference.security?" + page}
	return exec.Command("/usr/bin/open", address.String()).Run()
}
