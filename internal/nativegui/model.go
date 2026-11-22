//go:build gui

package nativegui

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/identity"
	"myapp/internal/trust"
)

type state struct {
	Ready        bool
	DeviceID     string
	DeviceName   string
	Role         config.Role
	Listen       string
	Peers        []config.Peer
	Clipboard    bool
	Files        bool
	Logging      bool
	LoggingLevel string
	SetupReason  string
}

func loadState(options Options) (state, error) {
	cfg, cfgErr := config.Load(options.ConfigPath)
	local, idErr := identity.Load(options.IdentityDirectory)
	if cfgErr != nil || idErr != nil {
		return state{SetupReason: setupReason(cfgErr, idErr)}, nil
	}
	return state{Ready: true, DeviceID: local.DeviceID, DeviceName: cfg.DeviceName, Role: cfg.Role, Listen: cfg.ListenAddress, Peers: cfg.Peers, Clipboard: cfg.Clipboard.TextEnabled, Files: cfg.Files.Enabled, Logging: cfg.Logging.Enabled, LoggingLevel: string(cfg.Logging.Level)}, nil
}
func setupReason(cfgErr, idErr error) string {
	if errors.Is(cfgErr, os.ErrNotExist) && errors.Is(idErr, os.ErrNotExist) {
		return "欢迎使用 MyApp，请设置这台电脑"
	}
	if errors.Is(cfgErr, os.ErrNotExist) {
		return "设备身份已存在，需要补充本机配置"
	}
	if errors.Is(idErr, os.ErrNotExist) {
		return "配置已存在，需要建立本机身份"
	}
	return "本机配置或身份文件无法读取，需要恢复设置"
}
func roleName(role config.Role) string {
	if role == config.RoleController {
		return "主电脑"
	}
	return "子电脑"
}
func emergencyShortcut() string {
	if runtime.GOOS == "darwin" {
		return "Control + Option + Esc"
	}
	return "Ctrl + Alt + Esc"
}
func peerName(id string, peers []desktop.Peer, records []trust.Record) string {
	for _, peer := range peers {
		if peer.ID == id && peer.Name != "" && peer.Name != id {
			return peer.Name
		}
	}
	for _, record := range records {
		if record.DeviceID == id && record.DeviceName != "" {
			return record.DeviceName
		}
	}
	if len(id) > 12 {
		return "电脑 " + id[:8]
	}
	return id
}
func connectionCount(view desktop.Snapshot) int {
	count := 0
	for _, peer := range view.Peers {
		if peer.Status == "connected" {
			count++
		}
	}
	return count
}
func taskLabel(state string) string {
	switch state {
	case "preparing":
		return "准备中"
	case "transferring":
		return "传输中"
	case "verifying":
		return "校验中"
	case "publishing":
		return "正在发布到剪贴板"
	case "completed":
		return "已完成"
	case "failed":
		return "失败"
	case "cancelling":
		return "正在取消"
	case "cancelled":
		return "已取消"
	case "unconfirmed":
		return "已发送，未获对端确认"
	default:
		return "等待状态"
	}
}
func formatBytes(value int64) string {
	if value < 1024 {
		return fmtBytes(value, "B", 1)
	}
	if value < 1<<20 {
		return fmtBytes(value, "KB", 1024)
	}
	if value < 1<<30 {
		return fmtBytes(value, "MB", 1<<20)
	}
	return fmtBytes(value, "GB", 1<<30)
}
func fmtBytes(value int64, unit string, divisor int64) string {
	if divisor == 1 {
		return fmt.Sprintf("%d %s", value, unit)
	}
	return fmt.Sprintf("%.1f %s", float64(value)/float64(divisor), unit)
}
func enabledText(enabled bool) string {
	if enabled {
		return "已启用"
	}
	return "已关闭"
}
