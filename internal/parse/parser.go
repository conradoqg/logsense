package parse

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"logsense/internal/model"
)

type Parser interface {
	Parse(line, source string) model.LogEntry
}

func NewParser(s model.Schema, forcedLayout string) (Parser, error) {
	if s.ParseStrategy == "json" {
		return &JSONParser{schema: s, layout: fallbackLayout(s.TimeLayout, forcedLayout)}, nil
	}
	if s.ParseStrategy == "logfmt" || s.ParseStrategy == "kv" {
		return &LogfmtParser{schema: s, layout: fallbackLayout(s.TimeLayout, forcedLayout)}, nil
	}
	// default regex
	return NewRegexParser(s, forcedLayout)
}

func fallbackLayout(a, forced string) string {
	if forced != "" {
		return forced
	}
	if a != "" {
		return a
	}
	return time.RFC3339
}

// JSON lines
type JSONParser struct {
	schema model.Schema
	layout string
}

func (p *JSONParser) Parse(line, source string) model.LogEntry {
	var m map[string]any
	_ = json.Unmarshal([]byte(line), &m)
	e := model.LogEntry{Raw: line, Fields: map[string]any{}, Source: source, FormatName: p.schema.FormatName}
	// Copy fields
	for k, v := range m {
		e.Fields[k] = v
	}
	// If there is a JSON-encoded payload inside a string field (e.g., k8s container logs with `log`),
	// attempt to parse and merge its keys for better column discovery.
	// Prefer common payload keys in order.
	innerKeys := []string{"log", "msg", "message"}
	for _, key := range innerKeys {
		if raw, ok := m[key]; ok {
			if s, ok := raw.(string); ok {
				t := strings.TrimSpace(s)
				if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
					var inner map[string]any
					if err := json.Unmarshal([]byte(t), &inner); err == nil {
						// Merge inner fields into entry fields (do not remove original wrapper field)
						for ik, iv := range inner {
							e.Fields[ik] = iv
						}
						// Best-effort timestamp/level from inner payload if not already set
						if e.Timestamp == nil {
							its := getStringPaths(inner, []string{"ts", "time", "timestamp"})
							if its != "" {
								if t, err := time.Parse(p.layout, its); err == nil {
									e.Timestamp = &t
								}
							}
						}
						if e.Level == "" {
							ilvl := strings.ToUpper(getStringPaths(inner, []string{"level", "lvl", "severity"}))
							if ilvl != "" {
								e.Level = normalizeLevel(p.schema, ilvl)
							}
						}
						break
					}
				}
			}
		}
	}
	// Best-effort timestamp and level
	ts := getStringPaths(m, []string{"ts", "time", "timestamp"})
	if ts != "" {
		if t, err := time.Parse(p.layout, ts); err == nil {
			e.Timestamp = &t
		}
	}
	lvl := strings.ToUpper(getStringPaths(m, []string{"level", "lvl", "severity"}))
	if lvl != "" {
		e.Level = normalizeLevel(p.schema, lvl)
	}
	return e
}

// Regex parser
type RegexParser struct {
	schema model.Schema
	layout string
	re     *regexp.Regexp
}

func NewRegexParser(s model.Schema, forced string) (Parser, error) {
	re, err := regexp.Compile(s.RegexPattern)
	if err != nil {
		return &RegexParser{schema: s, layout: fallbackLayout(s.TimeLayout, forced)}, nil
	}
	return &RegexParser{schema: s, layout: fallbackLayout(s.TimeLayout, forced), re: re}, nil
}

func (p *RegexParser) Parse(line, source string) model.LogEntry {
	e := model.LogEntry{Raw: line, Fields: map[string]any{}, Source: source, FormatName: p.schema.FormatName}
	if p.re == nil {
		e.Fields["msg"] = line
		return e
	}
	m := p.re.FindStringSubmatch(line)
	if m == nil {
		e.Fields["msg"] = line
		return e
	}
	names := p.re.SubexpNames()
	for i, name := range names {
		if i == 0 || name == "" {
			continue
		}
		val := m[i]
		e.Fields[name] = val
		if name == "ts" || name == "time" || name == "timestamp" {
			if t, err := time.Parse(p.layout, val); err == nil {
				e.Timestamp = &t
			}
		}
		if name == "level" || name == "lvl" || name == "severity" {
			e.Level = normalizeLevel(p.schema, strings.ToUpper(val))
		}
		if name == "status" {
			if n, err := strconv.Atoi(val); err == nil {
				e.Fields[name] = n
			}
		}
	}
	return e
}

// logfmt parser (very basic, supports quoted values)
type LogfmtParser struct {
	schema model.Schema
	layout string
}

func (p *LogfmtParser) Parse(line, source string) model.LogEntry {
	e := model.LogEntry{Raw: line, Fields: map[string]any{}, Source: source, FormatName: p.schema.FormatName}
	parts := splitLogfmt(line)
	for k, v := range parts {
		e.Fields[k] = v
	}
	ts := pick(parts, "ts", "time", "timestamp")
	if ts != "" {
		if t, err := time.Parse(p.layout, ts); err == nil {
			e.Timestamp = &t
		}
	}
	lvl := strings.ToUpper(pick(parts, "level", "lvl", "severity"))
	if lvl != "" {
		e.Level = normalizeLevel(p.schema, lvl)
	}
	return e
}

func pick(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return ""
}

func splitLogfmt(s string) map[string]string {
	res := map[string]string{}
	cur := ""
	inQuote := false
	key := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote && (c == ' ' || c == '\t') {
			if key != "" {
				res[key] = cur
				key, cur = "", ""
			}
			continue
		}
		if !inQuote && c == '=' {
			key = cur
			cur = ""
			continue
		}
		cur += string(c)
	}
	if key != "" {
		res[key] = cur
	}
	return res
}

func getStringPaths(m map[string]any, keys []string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func normalizeLevel(s model.Schema, lvl string) string {
	l := strings.ToUpper(strings.TrimSpace(lvl))
	for k, v := range s.LevelMapping {
		if strings.ToUpper(k) == l {
			return strings.ToUpper(v)
		}
	}
	switch l {
	case "TRACE":
		return "TRACE"
	case "DEBUG":
		return "DEBUG"
	case "INFO":
		return "INFO"
	case "WARN", "WARNING":
		return "WARN"
	case "ERROR", "ERR":
		return "ERROR"
	case "FATAL", "CRITICAL":
		return "FATAL"
	}
	return l
}
