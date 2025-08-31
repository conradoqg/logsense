package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
)

type Theme string

const (
	ThemeDark  Theme = "dark"
	ThemeLight Theme = "light"
)

type Config struct {
	FilePath         string
	UseStdin         bool
	Follow           bool
	MaxBuffer        int
	BlockSizeMB      int
	Theme            Theme
	Offline          bool
	NoCache          bool
	OpenAIModel      string
	OpenAIBase       string
	OpenAITimeoutSec int
	TimeLayout       string
	ForceFormat      string
	ExportFormat     string
	ExportOut        string

	// Internal
	IsPipedStdin bool
}

func Load() (*Config, error) {
	cfg := &Config{}

	// Detect if stdin is piped
	fi, _ := os.Stdin.Stat()
	cfg.IsPipedStdin = (fi.Mode() & os.ModeCharDevice) == 0

	fs := flag.NewFlagSet("logsense", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	fs.StringVar(&cfg.FilePath, "file", "", "path to log file")
	fs.BoolVar(&cfg.Follow, "follow", false, "follow file (tail -f)")
	fs.BoolVar(&cfg.UseStdin, "stdin", false, "read from stdin (default: auto if piped)")
	fs.IntVar(&cfg.MaxBuffer, "max-buffer", 200000, "ring buffer size (min 50000)")
	fs.IntVar(&cfg.BlockSizeMB, "block-size-mb", 0, "when reading a file (no follow), read only the last N MB instead of the whole file (0=all)")
	theme := string(ThemeDark)
	fs.StringVar(&theme, "theme", string(ThemeDark), "theme: dark|light")
	fs.BoolVar(&cfg.Offline, "offline", false, "disable OpenAI and work offline only")
	fs.BoolVar(&cfg.NoCache, "no-cache", false, "disable schema cache (skip read/write)")
	fs.StringVar(&cfg.OpenAIModel, "openai-model", getenvDefault("LOGSENSE_OPENAI_MODEL", "gpt-5-mini"), "OpenAI model override")
	fs.StringVar(&cfg.OpenAIBase, "openai-base-url", getenvDefault("LOGSENSE_OPENAI_BASE_URL", ""), "OpenAI base URL override")
	fs.IntVar(&cfg.OpenAITimeoutSec, "openai-timeout-sec", getenvDefaultInt("LOGSENSE_OPENAI_TIMEOUT_SEC", 120), "OpenAI request timeout in seconds")
	fs.StringVar(&cfg.TimeLayout, "time-layout", "", "force time layout (Go format)")
	fs.StringVar(&cfg.ForceFormat, "format", "", "force format: json|regex|logfmt|apache|syslog")
	fs.StringVar(&cfg.ExportFormat, "export", "", "export filtered view: csv|json")
	fs.StringVar(&cfg.ExportOut, "out", "", "output path for export")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return nil, err
	}
	cfg.Theme = Theme(theme)

	if cfg.ExportFormat != "" && cfg.ExportOut == "" {
		return nil, errors.New("--export requires --out path")
	}

	// Determine input source defaults
	if cfg.UseStdin || (cfg.IsPipedStdin && cfg.FilePath == "") {
		cfg.UseStdin = true
	}

	if !cfg.UseStdin && cfg.FilePath == "" {
		// No input: will run demo mode
	}

	if cfg.MaxBuffer < 50000 {
		cfg.MaxBuffer = 50000
	}

	return cfg, nil
}

func getenvDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func getenvDefaultInt(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}

func (c *Config) OpenAIKey() string { return os.Getenv("OPENAI_API_KEY") }

func (c *Config) String() string {
	return fmt.Sprintf("file=%s stdin=%v follow=%v theme=%s offline=%v", c.FilePath, c.UseStdin, c.Follow, c.Theme, c.Offline)
}
