//go:build gui && !darwin && !windows

package nativegui

import (
	"myapp/internal/config"
	"os"
)

func checkPermissions(config.Role) []permission {
	state := "unavailable"
	if os.Getenv("DISPLAY") != "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		state = "supported"
	}
	return []permission{{Name: "X11 键鼠共享", State: state, Description: "Wayland 输入授权尚未接入，不能开始键鼠控制"}}
}
