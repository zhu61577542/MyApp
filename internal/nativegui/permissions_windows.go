//go:build gui && windows

package nativegui

import "myapp/internal/config"

func checkPermissions(config.Role) []permission {
	return []permission{{Name: "普通桌面控制", State: "supported", Description: "仅控制已登录的普通桌面；不控制锁屏、UAC 安全桌面或更高权限应用"}}
}
