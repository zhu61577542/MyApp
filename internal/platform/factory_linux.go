//go:build linux

package platform

import (
	"errors"
	"os"

	"myapp/internal/clipboard"
	"myapp/internal/fileclipboard"
	common "myapp/internal/input"
	linuxclipboard "myapp/internal/platform/linux/clipboard"
	linuxinput "myapp/internal/platform/linux/input"
)

func NewInjector() (common.Injector, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return nil, errors.New("当前 Wayland 会话没有可用的远程桌面 portal 输入授权，请改用 X11 会话")
	}
	return linuxinput.New("")
}

func NewCapturer() (common.Capturer, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return nil, errors.New("当前 Wayland 会话没有可用的远程桌面 portal 输入授权，请改用 X11 会话")
	}
	return linuxinput.NewCapturer("")
}

func NewClipboard() (clipboard.Backend, error)         { return linuxclipboard.New() }
func NewFileClipboard() (fileclipboard.Backend, error) { return linuxclipboard.NewFiles() }
