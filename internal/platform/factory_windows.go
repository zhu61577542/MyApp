//go:build windows

package platform

import (
	"myapp/internal/clipboard"
	"myapp/internal/fileclipboard"
	common "myapp/internal/input"
	windowsclipboard "myapp/internal/platform/windows/clipboard"
	wininput "myapp/internal/platform/windows/input"
)

func NewInjector() (common.Injector, error)            { return wininput.New() }
func NewCapturer() (common.Capturer, error)            { return wininput.NewCapturer() }
func NewClipboard() (clipboard.Backend, error)         { return windowsclipboard.New() }
func NewFileClipboard() (fileclipboard.Backend, error) { return windowsclipboard.NewFiles() }
