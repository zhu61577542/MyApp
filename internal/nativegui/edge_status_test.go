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
