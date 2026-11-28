package desktop

import "time"

type Diagnostic struct {
	Time   time.Time      `json:"time"`
	Event  string         `json:"event"`
	Fields map[string]any `json:"fields,omitempty"`
}

func (s *Session) Diagnostic(event string, fields map[string]any) {
	if s == nil {
		return
	}
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	if len(s.diagnostics) == 128 {
		s.diagnosticsDropped++
		return
	}
	s.diagnostics = append(s.diagnostics, Diagnostic{Time: time.Now().UTC(), Event: event, Fields: fields})
}

func (s *Session) DrainDiagnostics() []Diagnostic {
	s.diagnosticMu.Lock()
	defer s.diagnosticMu.Unlock()
	result := s.diagnostics
	s.diagnostics = nil
	if s.diagnosticsDropped > 0 {
		result = append(result, Diagnostic{Time: time.Now().UTC(), Event: "诊断队列溢出", Fields: map[string]any{"dropped": s.diagnosticsDropped}})
		s.diagnosticsDropped = 0
	}
	return result
}
