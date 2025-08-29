package logx

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

var (
	mu       sync.Mutex
	level    = Info
	buf      = make([]string, 0, 500)
	maxLines = 500
	// default to no stderr output to avoid breaking TUIs; enable via LOGSENSE_LOG_STDERR=1
	toStderr = false
)

func SetLevel(l Level) { mu.Lock(); level = l; mu.Unlock() }

func SetLevelFromEnv() {
	lv := strings.ToLower(strings.TrimSpace(os.Getenv("LOGSENSE_LOG_LEVEL")))
	switch lv {
	case "debug":
		SetLevel(Debug)
	case "info":
		SetLevel(Info)
	case "warn", "warning":
		SetLevel(Warn)
	case "error":
		SetLevel(Error)
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("LOGSENSE_LOG_STDERR"))); v != "" {
		toStderr = v != "0" && v != "false" && v != "no"
	}
}

func Debugf(format string, a ...any) { logf(Debug, "DEBUG", format, a...) }
func Infof(format string, a ...any)  { logf(Info, "INFO", format, a...) }
func Warnf(format string, a ...any)  { logf(Warn, "WARN", format, a...) }
func Errorf(format string, a ...any) { logf(Error, "ERROR", format, a...) }

func logf(l Level, tag, format string, a ...any) {
	mu.Lock()
	defer mu.Unlock()
	if l < level {
		return
	}
	ts := time.Now().Format("2006-01-02T15:04:05.000Z07:00")
	line := fmt.Sprintf("%s %-5s %s", ts, tag, fmt.Sprintf(format, a...))
	if len(buf) >= maxLines {
		// drop oldest
		copy(buf[0:], buf[1:])
		buf = buf[:len(buf)-1]
	}
	buf = append(buf, line)
	if toStderr {
		fmt.Fprintln(os.Stderr, line)
	}
}

func Dump() string {
	mu.Lock()
	defer mu.Unlock()
	return strings.Join(buf, "\n")
}

func Lines() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, len(buf))
	copy(out, buf)
	return out
}
