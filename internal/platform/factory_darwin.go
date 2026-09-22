//go:build darwin

package platform

import (
	"errors"

	"myapp/internal/clipboard"
	"myapp/internal/fileclipboard"
	common "myapp/internal/input"
	darwinclipboard "myapp/internal/platform/darwin/clipboard"
	darwininput "myapp/internal/platform/darwin/input"
)

func NewInjector() (common.Injector, error) {
	injector, err := darwininput.New()
	if err != nil {
		return nil, err
	}
	if !injector.CanInject() {
		return nil, errors.New("macOS 辅助功能权限未授权")
	}
	return injector, nil
}

func NewCapturer() (common.Capturer, error) {
	capturer, err := darwininput.NewCapturer()
	if err != nil {
		return nil, err
	}
	if !capturer.CanCapture() {
		return nil, errors.New("macOS 输入监控权限未授权")
	}
	return capturer, nil
}
func NewClipboard() (clipboard.Backend, error)         { return darwinclipboard.New(), nil }
func NewFileClipboard() (fileclipboard.Backend, error) { return darwinclipboard.NewFiles(), nil }
