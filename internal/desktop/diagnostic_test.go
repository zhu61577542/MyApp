package desktop

import "testing"

func TestDiagnosticQueueKeepsTransitionsAndReportsOverflow(t *testing.T) {
	s := &Session{}
	for n := range 130 {
		s.Diagnostic("控制权变化", map[string]any{"epoch": n})
	}
	got := s.DrainDiagnostics()
	if len(got) != 129 || got[0].Fields["epoch"] != 0 || got[127].Fields["epoch"] != 127 || got[128].Fields["dropped"] != uint64(2) {
		t.Fatal("诊断队列未保留顺序或未报告溢出")
	}
	if len(s.DrainDiagnostics()) != 0 {
		t.Fatal("已读诊断被重放")
	}
}
