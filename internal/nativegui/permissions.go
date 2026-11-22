//go:build gui

package nativegui

type permission struct {
	Name        string
	State       string
	Description string
	Page        string
}

func permissionLabel(state string) string {
	switch state {
	case "allowed":
		return "已允许"
	case "denied":
		return "未授权"
	case "supported":
		return "受支持"
	case "unavailable":
		return "不可用"
	default:
		return "尚未检测"
	}
}
