package parse

import (
	"logsense/internal/model"
	"testing"
)

func TestJSONParser(t *testing.T) {
	s := model.Schema{FormatName: "json_lines", ParseStrategy: "json", TimeLayout: "2006-01-02T15:04:05Z07:00"}
	p, _ := NewParser(s, "")
	e := p.Parse(`{"ts":"2025-01-01T12:00:00Z","level":"info","msg":"ok"}`, "stdin")
	if e.Level != "INFO" {
		t.Fatalf("level: %s", e.Level)
	}
	if e.Timestamp == nil {
		t.Fatalf("timestamp nil")
	}
}

func TestLogfmtParser(t *testing.T) {
	s := model.Schema{FormatName: "logfmt", ParseStrategy: "logfmt", TimeLayout: "2006-01-02T15:04:05Z07:00"}
	p, _ := NewParser(s, "")
	e := p.Parse(`time=2025-01-01T12:00:00Z level=warn msg="ok"`, "stdin")
	if e.Level != "WARN" {
		t.Fatalf("level: %s", e.Level)
	}
}
