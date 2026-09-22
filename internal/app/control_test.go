package app

import (
	"errors"
	"testing"

	"myapp/internal/config"
)

func TestControlLifecycleAndStaleMessages(t *testing.T) {
	machine, err := NewControlMachine(config.RoleController)
	if err != nil {
		t.Fatal(err)
	}
	enter, err := machine.BeginEnter("peer-a")
	if err != nil || enter.State != StateEntering {
		t.Fatalf("开始切换失败: %+v %v", enter, err)
	}
	if _, err := machine.ConfirmEnter(enter.Generation-1, "peer-a"); !errors.Is(err, ErrStaleAction) {
		t.Fatalf("旧确认未被拒绝: %v", err)
	}
	remote, err := machine.ConfirmEnter(enter.Generation, "peer-a")
	if err != nil || remote.State != StateRemote {
		t.Fatalf("确认切换失败: %+v %v", remote, err)
	}
	recovery, err := machine.BeginRecovery("连接断开")
	if err != nil || recovery.State != StateRecovering {
		t.Fatalf("恢复失败: %+v %v", recovery, err)
	}
	release, err := machine.LocalRestored(recovery.Generation)
	if err != nil || release.TargetID != "peer-a" {
		t.Fatalf("本地恢复失败: %+v %v", release, err)
	}
	if snapshot := machine.Snapshot(); snapshot.State != StateLocal || snapshot.TargetID != "" {
		t.Fatalf("最终状态错误: %+v", snapshot)
	}
}

func TestAgentCannotControl(t *testing.T) {
	machine, _ := NewControlMachine(config.RoleAgent)
	if _, err := machine.BeginEnter("controller"); !errors.Is(err, ErrNotController) {
		t.Fatalf("受控端发起控制错误=%v", err)
	}
}
