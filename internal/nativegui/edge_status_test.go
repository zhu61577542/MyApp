//go:build gui

package nativegui

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/instance"
)

func TestCrossingSummaryRequiresLocalMonitorAndShowsFailure(t *testing.T) {
	v := &desktopView{cfg: config.Default("source", config.RoleController), services: &serviceManager{cmd: &exec.Cmd{}}, view: desktop.Snapshot{EdgeEnabled: true, Peers: []desktop.Peer{{Status: "connected", Control: true, Edge: true}}}}
	title, _, _, _ := v.controlSummary()
	if title != "正在准备跨屏" {
		t.Fatal("本机监视未就绪仍显示可跨屏", title)
	}
	v.view.EdgeReady = true
	v.view.EdgeError = "捕获失败"
	title, _, _, _ = v.controlSummary()
	if title != "跨屏暂不可用" {
		t.Fatal("隐藏了真实捕获错误", title)
	}
}

func TestDuplicateGUIReturnsBeforeCreatingWindowOrService(t *testing.T) {
	guard, err := instance.Acquire("desktop-gui")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if err := Run(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-guard.Activations:
	case <-time.After(time.Second):
		t.Fatal("重复启动没有唤起已有实例")
	}
}

func TestPeerSummaryUsesActualInputSourceAndReadiness(t *testing.T) {
	v := &desktopView{cfg: config.Default("windows", config.RoleAgent), services: &serviceManager{cmd: &exec.Cmd{}}, view: desktop.Snapshot{DeviceID: "windows", PeerMode: true, Target: "mac", ControlPhase: "active", ControlSource: "windows"}}
	title, _, _, _ := v.controlSummary()
	if title != "正在控制 mac" {
		t.Fatal("按固定主副角色显示了错误控制方向", title)
	}
	v.cfg.Role = config.RoleController
	v.view.ControlSource = "mac"
	title, _, _, _ = v.controlSummary()
	if title != "正在接受 mac 的控制" {
		t.Fatal(title)
	}
	v.cfg.Role = config.RoleAgent
	v.view.Target = ""
	v.view.EdgeEnabled = true
	v.view.EdgeReady = true
	v.view.Accepting = true
	v.view.Peers = []desktop.Peer{{ID: "mac", Status: "connected"}}
	title, _, _, _ = v.controlSummary()
	if title == "双向跨屏已就绪" {
		t.Fatal("未确认平台能力就显示双向已就绪")
	}
	v.view.PeerEdgesReady = true
	title, _, _, _ = v.controlSummary()
	if title != "双向跨屏已就绪" {
		t.Fatal(title)
	}
}
