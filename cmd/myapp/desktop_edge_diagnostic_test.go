package main

import (
	"testing"
	"time"

	"myapp/internal/config"
	"myapp/internal/desktop"
	"myapp/internal/pointer"
)

func TestEdgeDiagnosticsExposeIdleGateWithoutInputContent(t *testing.T) {
	s := desktop.New(config.Default("Windows", config.RoleAgent), "w")
	c := &desktopCapture{state: s, activity: &desktopActivity{ready: true}}
	d := &edgeDiagnostic{side: "right"}
	screen := pointer.Screen{ID: "desktop", X: -1080, Y: -493, Width: 3640, Height: 1933}
	d.record("no-physical-movement")
	d.position(pointer.Point{X: 2559, Y: 500}, screen)
	d.report(c, s.Sharing(), screen)
	got := s.DrainDiagnostics()
	if len(got) != 1 || got[0].Event != "实体边缘监视" {
		t.Fatal("缺少Local诊断")
	}
	f := got[0].Fields
	if f["listener_ready"] != true || f["physical_moves"] != uint64(0) || f["side"] != "right" {
		t.Fatal("未区分就绪与实体移动")
	}
	if _, ok := f["x"]; ok {
		t.Fatal("不应记录指针坐标")
	}
	d.record("edge-not-triggered")
	d.position(pointer.Point{X: 2559, Y: 500}, screen)
	if f["samples"].(map[string]uint64)["edge-not-triggered"] != 0 || f["edge_samples"].(map[string]uint64)["right"] != 1 {
		t.Fatal("诊断快照仍共享可变计数")
	}
	d.report(c, s.Sharing(), screen)
	if len(s.DrainDiagnostics()) != 0 {
		t.Fatal("未限频")
	}
	d.next = time.Time{}
	d.report(c, s.Sharing(), screen)
	if len(s.DrainDiagnostics()) != 1 {
		t.Fatal("未持续输出诊断")
	}
	changed := screen
	changed.ID = "new-layout"
	changed.Width = 5120
	d.next = time.Time{}
	d.report(c, s.Sharing(), changed)
	newReport := s.DrainDiagnostics()
	if len(newReport) != 1 || len(newReport[0].Fields["edge_samples"].(map[string]uint64)) != 0 || len(newReport[0].Fields["minimum_edge_distance"].(map[string]int)) != 0 {
		t.Fatal("新布局沿用了旧边缘统计")
	}
}
