package detect

import (
	"bufio"
	"os"
	"testing"
)

func readLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	out := []string{}
	for s.Scan() {
		out = append(out, s.Text())
		if len(out) >= n {
			break
		}
	}
	return out
}

func TestHeuristicsJSON(t *testing.T) {
	g := Heuristics(readLines("../../testdata/json_lines.ndjson", 10))
	if g.Schema.FormatName != "json_lines" {
		t.Fatalf("expected json_lines, got %s", g.Schema.FormatName)
	}
}

func TestHeuristicsLogfmt(t *testing.T) {
	g := Heuristics(readLines("../../testdata/logfmt.log", 10))
	if g.Schema.FormatName != "logfmt" {
		t.Fatalf("expected logfmt, got %s", g.Schema.FormatName)
	}
}
