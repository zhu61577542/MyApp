//go:build gui && !darwin

package nativegui

import (
	"context"
	"errors"
	"myapp/internal/config"
)

func requestPlatformPermissions(config.Role) error {
	return errors.New("当前平台不使用 macOS 权限请求")
}
func resetPlatformPermissions(context.Context) error {
	return errors.New("当前平台不使用 macOS 权限重置")
}
