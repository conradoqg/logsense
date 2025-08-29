package detect

import (
	"regexp"
	"strings"
	"time"

	"logsense/internal/model"
)

var (
	reApacheCombined = regexp.MustCompile(`^\S+ \S+ \S+ \[[^\]]+\] "[A-Z]+ [^\s]+ [^"]+" \d{3} \d+ "[^"]*" "[^"]*"`)
	reSyslogRFC5424  = regexp.MustCompile(`^<\d+>1 \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`)
	reLogfmtKV       = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*=`)
)

type Guess struct {
	Schema     model.Schema
	Confidence float64
}

// Quick offline heuristics on a small sample.
func Heuristics(sample []string) Guess {
	lines := 0
	jsonCount := 0
	logfmtCount := 0
	apacheCount := 0
	syslogCount := 0
	for _, l := range sample {
		s := strings.TrimSpace(l)
		if s == "" {
			continue
		}
		lines++
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			jsonCount++
		}
		if reLogfmtKV.MatchString(s) && strings.Contains(s, "=") {
			logfmtCount++
		}
		if reApacheCombined.MatchString(s) {
			apacheCount++
		}
		if reSyslogRFC5424.MatchString(s) {
			syslogCount++
		}
	}
	// Choose highest
	if jsonCount > logfmtCount && jsonCount > apacheCount && jsonCount > syslogCount && jsonCount >= lines/2 {
		return Guess{Schema: jsonSchema(), Confidence: conf(lines, jsonCount)}
	}
	if logfmtCount >= apacheCount && logfmtCount >= syslogCount && logfmtCount >= lines/2 {
		return Guess{Schema: logfmtSchema(), Confidence: conf(lines, logfmtCount)}
	}
	if apacheCount >= syslogCount && apacheCount > 0 {
		return Guess{Schema: apacheSchema(), Confidence: conf(lines, apacheCount)}
	}
	if syslogCount > 0 {
		return Guess{Schema: syslogSchema(), Confidence: conf(lines, syslogCount)}
	}
	// Unknown
	return Guess{Schema: unknownSchema(), Confidence: 0.0}
}

func conf(lines, hits int) float64 {
	if lines == 0 {
		return 0
	}
	return float64(hits) / float64(lines)
}

func jsonSchema() model.Schema {
	return model.Schema{
		FormatName:    "json_lines",
		ParseStrategy: "json",
		TimeLayout:    time.RFC3339,
		LevelMapping:  map[string]string{"warn": "WARN", "warning": "WARN", "info": "INFO", "error": "ERROR", "debug": "DEBUG", "fatal": "FATAL", "trace": "TRACE"},
		Fields:        []model.FieldDef{{Name: "ts", Type: "string", Description: "timestamp", PathOrGroup: ".ts"}, {Name: "time", Type: "string", Description: "timestamp", PathOrGroup: ".time"}, {Name: "level", Type: "string", Description: "level", PathOrGroup: ".level"}, {Name: "msg", Type: "string", Description: "message", PathOrGroup: ".msg"}, {Name: "message", Type: "string", Description: "message", PathOrGroup: ".message"}},
		Confidence:    0.8,
	}
}

func logfmtSchema() model.Schema {
	return model.Schema{
		FormatName:    "logfmt",
		ParseStrategy: "logfmt",
		TimeLayout:    time.RFC3339,
		LevelMapping:  map[string]string{"warn": "WARN", "warning": "WARN", "info": "INFO", "error": "ERROR", "debug": "DEBUG", "fatal": "FATAL", "trace": "TRACE"},
		Fields:        []model.FieldDef{{Name: "time", Type: "string", Description: "time", PathOrGroup: "time"}, {Name: "level", Type: "string", Description: "level", PathOrGroup: "level"}, {Name: "msg", Type: "string", Description: "message", PathOrGroup: "msg"}},
		Confidence:    0.6,
	}
}

func apacheSchema() model.Schema {
	return model.Schema{
		FormatName:    "apache_combined",
		ParseStrategy: "regex",
		RegexPattern:  `^(?P<ip>\S+) \S+ \S+ \[(?P<ts>[^\]]+)\] "(?P<method>[A-Z]+) (?P<path>[^\s]+) [^"]+" (?P<status>\d{3}) (?P<size>\d+) "(?P<ref>[^"]*)" "(?P<ua>[^"]*)"`,
		TimeLayout:    "02/Jan/2006:15:04:05 -0700",
		LevelMapping:  map[string]string{},
		Fields:        []model.FieldDef{{Name: "ts", Type: "string", Description: "timestamp", PathOrGroup: "ts"}, {Name: "status", Type: "int", Description: "status", PathOrGroup: "status"}, {Name: "method", Type: "string", Description: "method", PathOrGroup: "method"}, {Name: "path", Type: "string", Description: "path", PathOrGroup: "path"}, {Name: "ip", Type: "string", Description: "client ip", PathOrGroup: "ip"}},
		Confidence:    0.6,
	}
}

func syslogSchema() model.Schema {
	return model.Schema{
		FormatName:    "syslog_rfc5424",
		ParseStrategy: "regex",
		RegexPattern:  `^<(?P<pri>\d+)>1 (?P<ts>\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})) (?P<host>\S+) (?P<app>\S+) \S+ \S+ - (?P<msg>.*)$`,
		TimeLayout:    time.RFC3339,
		LevelMapping:  map[string]string{},
		Fields:        []model.FieldDef{{Name: "ts", Type: "string", Description: "timestamp", PathOrGroup: "ts"}, {Name: "app", Type: "string", Description: "app", PathOrGroup: "app"}, {Name: "msg", Type: "string", Description: "message", PathOrGroup: "msg"}},
		Confidence:    0.6,
	}
}

func unknownSchema() model.Schema {
	return model.Schema{FormatName: "unknown", ParseStrategy: "regex", RegexPattern: `^(?P<msg>.*)$`, Fields: []model.FieldDef{{Name: "msg", Type: "string", Description: "message", PathOrGroup: "msg"}}}
}
