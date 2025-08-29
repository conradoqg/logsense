package model

import (
	"encoding/json"
	"sort"
	"sync"
	"time"
)

type LogEntry struct {
	Raw        string         `json:"raw"`
	Fields     map[string]any `json:"fields"`
	Timestamp  *time.Time     `json:"ts,omitempty"`
	Level      string         `json:"level,omitempty"`
	Source     string         `json:"source,omitempty"`
	FormatName string         `json:"formatName,omitempty"`
	SchemaVer  string         `json:"schemaVersion,omitempty"`
}

type FieldDef struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	PathOrGroup string `json:"pathOrGroup"`
}

type Schema struct {
	FormatName      string            `json:"formatName"`
	ProbableSources []string          `json:"probableSources"`
	ParseStrategy   string            `json:"parseStrategy"` // json|regex|logfmt|kv|csv
	TimeLayout      string            `json:"timeLayout"`
	LevelMapping    map[string]string `json:"levelMapping"`
	RegexPattern    string            `json:"regexPattern,omitempty"`
	Fields          []FieldDef        `json:"fields"`
	Confidence      float64           `json:"confidence"`
	SampleParsedRow map[string]any    `json:"sampleParsedRow"`
}

func (s Schema) ColumnOrder() []string {
	// Preferred columns
	pref := []string{"ts", "time", "timestamp", "level", "lvl", "severity", "source", "component", "msg", "message"}
	cols := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		cols = append(cols, f.Name)
	}
	// Stable sort by preference
	sort.SliceStable(cols, func(i, j int) bool {
		pi := indexOf(pref, cols[i])
		pj := indexOf(pref, cols[j])
		if pi == pj {
			return cols[i] < cols[j]
		}
		return pi < pj
	})
	return cols
}

func indexOf(arr []string, s string) int {
	for i, v := range arr {
		if v == s {
			return i
		}
	}
	return len(arr) + 1
}

// Ring buffer for LogEntry
type Ring struct {
	mu      sync.RWMutex
	buf     []LogEntry
	cap     int
	start   int
	size    int
	total   uint64 // total ingested
	dropped uint64
}

func NewRing(capacity int) *Ring {
	return &Ring{cap: capacity, buf: make([]LogEntry, capacity)}
}

func (r *Ring) Push(e LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size < r.cap {
		r.buf[(r.start+r.size)%r.cap] = e
		r.size++
	} else {
		// overwrite oldest
		r.buf[r.start] = e
		r.start = (r.start + 1) % r.cap
		r.dropped++
	}
	r.total++
}

func (r *Ring) Snapshot() ([]LogEntry, uint64, uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]LogEntry, r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.buf[(r.start+i)%r.cap]
	}
	return out, r.total, r.dropped
}

func (r *Ring) ClearVisible() { // does not reset counters
	r.mu.Lock()
	defer r.mu.Unlock()
	r.size = 0
	r.start = 0
}

func (e LogEntry) PrettyJSON() string {
	b, _ := json.MarshalIndent(e.Fields, "", "  ")
	return string(b)
}
