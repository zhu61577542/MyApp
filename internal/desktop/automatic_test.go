package desktop

import (
	"myapp/internal/config"
	"testing"
)

func TestTrustedControllerAutomaticallyPreparesCrossingWithLegacyConfig(t *testing.T) {
	cfg := config.Default("Mac", config.RoleController)
	cfg.Switch.EdgeEnabled = false
	cfg.Peers = []config.Peer{{ID: "windows", Address: "192.168.1.2:24800"}}
	if !New(cfg, "mac").Snapshot().EdgeEnabled {
		t.Fatal("旧配置不应要求额外启用跨屏")
	}
	cfg.Role = config.RoleAgent
	if New(cfg, "windows").Snapshot().EdgeEnabled {
		t.Fatal("子电脑不应主动捕获主电脑")
	}
}

func TestPermissionPreflightNeverCreatesOSNotification(t *testing.T) {
	var gate NotificationGate
	for range 100 {
		for _, key := range []string{"permissions", "permission:输入监控", "permission:控制键鼠"} {
			if gate.Take(key) {
				t.Fatal("权限预检不得发送系统通知")
			}
		}
	}
	if !gate.Take("task:a") || gate.Take("task:a") {
		t.Fatal("同一任务重复通知")
	}
	if !gate.Take("connection") || gate.Take("connection") {
		t.Fatal("重连重复通知")
	}
	gate.Resolved("connection")
	if !gate.Take("connection") {
		t.Fatal("真正恢复后新故障应可通知")
	}
}

func TestReconnectPreservesAutomaticAndEmergencyIntent(t *testing.T) {
	s := New(config.Default("Mac", config.RoleController), "mac")
	s.Disconnected("network")
	if !s.Sharing().EdgeEnabled {
		t.Fatal("短暂断线丢失自动跨屏状态")
	}
	s.Update(func(snapshot *Snapshot) { snapshot.EdgeEnabled = false })
	s.Disconnected("network")
	if s.Sharing().EdgeEnabled {
		t.Fatal("重连覆盖紧急退出或位置编辑")
	}
}
