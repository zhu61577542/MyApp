package desktop

import (
	"myapp/internal/config"
	"testing"
)

func TestEdgeFailureSurvivesConnectionStatusAndDeduplicates(t *testing.T) {
	s := New(config.Default("source", config.RoleController), "source")
	s.ReportEdge(false, "跨屏启动失败", "输入监控未授权")
	revision := s.Snapshot().Revision
	s.ReportEdge(false, "跨屏启动失败", "输入监控未授权")
	if s.Snapshot().Revision != revision {
		t.Fatal("重复诊断刷写状态")
	}
	s.Update(func(snapshot *Snapshot) { snapshot.Error = ""; snapshot.Phase = "connected" })
	if s.Snapshot().EdgeError == "" {
		t.Fatal("重连状态清除了跨屏错误")
	}
}
