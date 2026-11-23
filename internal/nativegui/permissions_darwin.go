//go:build gui && darwin

package nativegui

import (
	"myapp/internal/config"
	darwininput "myapp/internal/platform/darwin/input"
	"sync"
)

var permissionProbes struct {
	once     sync.Once
	capture  *darwininput.Capturer
	injector *darwininput.Injector
}

func checkPermissions(role config.Role) []permission {
	permissionProbes.once.Do(func() {
		permissionProbes.capture, _ = darwininput.NewCapturer()
		permissionProbes.injector, _ = darwininput.New()
	})
	var result []permission
	if role == config.RoleController {
		state := "unknown"
		capture := permissionProbes.capture
		if capture != nil {
			state = "denied"
			if capture.CanCapture() {
				state = "allowed"
			}
		}
		result = append(result, permission{Name: "输入监控", State: state, Description: "读取本机键鼠操作，用于控制其他电脑", Page: "Privacy_ListenEvent"})
	}
	state := "unknown"
	injector := permissionProbes.injector
	if injector != nil {
		state = "denied"
		if injector.CanInject() {
			state = "allowed"
		}
	}
	result = append(result, permission{Name: "控制键鼠", State: state, Description: "系统可能称为“辅助功能”或“设备控制和数据访问”", Page: "Privacy_Accessibility"})
	result = append(result, permission{Name: "本地网络", State: "unknown", Description: "发现或连接设备时由系统请求；没有通用查询接口，且当前系统不支持程序重置此项。", Page: "Privacy_LocalNetwork"})
	return result
}
