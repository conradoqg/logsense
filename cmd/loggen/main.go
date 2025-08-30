package main

import (
    "bufio"
    "encoding/json"
    "flag"
    "fmt"
    "math/rand"
    "os"
    "os/signal"
    "path/filepath"
    "strings"
    "sync"
    "sync/atomic"
    "syscall"
    "time"
)

// Supported formats (simplified)
const (
    formatText       = "text"
    formatJSONLines  = "json_lines"
)

func main() {
	var (
		formatsCSV  string
		format      string
		rate        float64
		outPath     string
		toStdout    bool
		durationStr string
	)

    flag.StringVar(&formatsCSV, "formats", "", "Comma-separated list: text,json_lines. Generates each to simulateddata/<format>.log")
    flag.StringVar(&format, "format", "", "Single format: text or json_lines. Use with --stdout or --out")
	flag.Float64Var(&rate, "rate", 5.0, "Messages per second per stream")
	flag.StringVar(&outPath, "out", "", "Output file path (only when --format is set). Defaults to simulateddata/<format>.log")
	flag.BoolVar(&toStdout, "stdout", false, "Write to stdout instead of file (only when --format is set)")
	flag.StringVar(&durationStr, "duration", "", "Optional run duration (e.g., 30s, 2m). Empty means run until interrupted")
	flag.Parse()

	rand.Seed(time.Now().UnixNano())

    // Setup interrupt handling
    var interrupted atomic.Bool
    abort := make(chan struct{})
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
    go func() {
        <-sigCh
        interrupted.Store(true)
        // Signal all streams to stop
        close(abort)
    }()

    // Parse duration if provided
    var deadline time.Time
    if durationStr != "" {
        d, err := time.ParseDuration(durationStr)
        if err != nil {
            fmt.Fprintf(os.Stderr, "invalid duration: %v\n", err)
            os.Exit(2)
        }
        deadline = time.Now().Add(d)
    }

    // Helper to check if we should stop
    shouldStop := func() bool {
        // Stop on interrupt or when deadline passes
        select {
        case <-abort:
            return true
        default:
        }
        if !deadline.IsZero() && time.Now().After(deadline) {
            return true
        }
        return false
    }

	// If formatsCSV is set, we generate to files under simulateddata
    if formatsCSV != "" {
        formats := splitFormats(formatsCSV)
        if len(formats) == 0 {
            fmt.Fprintln(os.Stderr, "no valid formats provided")
            os.Exit(2)
        }
        dir := filepath.Join("simulateddata")
        if err := os.MkdirAll(dir, 0o755); err != nil {
            fmt.Fprintf(os.Stderr, "failed to create simulateddata: %v\n", err)
            os.Exit(1)
        }
        var wg sync.WaitGroup
        var created []string
        for _, f := range formats {
            p := filepath.Join(dir, f+".log")
            if err := runStreamToFile(&wg, f, p, rate, shouldStop); err != nil {
                fmt.Fprintf(os.Stderr, "error starting %s stream: %v\n", f, err)
                os.Exit(1)
            }
            created = append(created, p)
            fmt.Fprintf(os.Stderr, "generating %s logs -> %s at %.2f msg/s\n", f, p, rate)
        }
        wg.Wait()
        // If interrupted, remove created files
        if interrupted.Load() {
            for _, p := range created {
                _ = os.Remove(p)
            }
        }
        return
    }

	// Single format mode
	if format == "" {
		fmt.Fprintln(os.Stderr, "either --formats or --format is required")
		os.Exit(2)
	}
	format = normalizeFormat(format)
    if !isSupported(format) {
        fmt.Fprintf(os.Stderr, "unsupported format: %s\n", format)
        os.Exit(2)
    }

	if toStdout {
		w := bufio.NewWriter(os.Stdout)
		defer w.Flush()
		runStream(w, format, rate, shouldStop)
		return
	}

	// default to simulateddata/<format>.log if outPath is empty
    if outPath == "" {
        if err := os.MkdirAll("simulateddata", 0o755); err != nil {
            fmt.Fprintf(os.Stderr, "failed to create simulateddata: %v\n", err)
            os.Exit(1)
        }
        outPath = filepath.Join("simulateddata", format+".log")
    }
    var wg sync.WaitGroup
    if err := runStreamToFile(&wg, format, outPath, rate, shouldStop); err != nil {
        fmt.Fprintf(os.Stderr, "error: %v\n", err)
        os.Exit(1)
    }
    fmt.Fprintf(os.Stderr, "generating %s logs -> %s at %.2f msg/s\n", format, outPath, rate)
    wg.Wait()
    // If interrupted, remove the created file
    if interrupted.Load() {
        _ = os.Remove(outPath)
    }
}

func splitFormats(csv string) []string {
	parts := strings.Split(csv, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = normalizeFormat(p)
		if isSupported(p) {
			out = append(out, p)
		}
	}
	return out
}

func normalizeFormat(f string) string {
    f = strings.ToLower(strings.TrimSpace(f))
    switch f {
    case "json", "ndjson", "jsonl":
        return formatJSONLines
    case "plain", "txt":
        return formatText
    default:
        return f
    }
}

func isSupported(f string) bool {
    switch f {
    case formatText, formatJSONLines:
        return true
    default:
        return false
    }
}

func runStreamToFile(wg *sync.WaitGroup, format, path string, rate float64, shouldStop func() bool) error {
    // Always clear the existing log at the start
    f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
    if err != nil {
        return err
    }
    w := bufio.NewWriter(f)
    wg.Add(1)
    go func() {
        defer wg.Done()
        defer w.Flush()
        defer f.Close()
        runStream(w, format, rate, shouldStop)
    }()
    return nil
}

func runStream(w *bufio.Writer, format string, rate float64, shouldStop func() bool) {
    if rate <= 0 {
        rate = 1
    }
    // interval between messages
    interval := time.Duration(float64(time.Second) / rate)
    if interval <= 0 {
        interval = time.Millisecond
    }
    ticker := time.NewTicker(interval)
    defer ticker.Stop()

    // Create a generator that picks a schema/template at start
    lineFn := newLineGenerator(format)
    for {
        if shouldStop() {
            return
        }
        select {
        case <-ticker.C:
            line := lineFn()
            w.WriteString(line)
            w.WriteByte('\n')
            _ = w.Flush()
        }
    }
}
// newLineGenerator returns a function that generates a line.
// For json_lines, it picks a random schema once and sticks to it for the stream.
// For text, it picks one template and uses it for all lines.
func newLineGenerator(format string) func() string {
    switch format {
    case formatJSONLines:
        schema := newRandomJSONSchema()
        return func() string { return schema.randomRecordJSON() }
    case formatText:
        tpl := pickTextTemplate()
        return func() string { return renderTextTemplate(tpl) }
    default:
        return func() string { return "" }
    }
}


func randIP() string {
	return fmt.Sprintf("%d.%d.%d.%d", rand.Intn(223)+1, rand.Intn(255), rand.Intn(255), rand.Intn(255))
}

func randomUser() string {
	users := []string{"-", "alice", "bob", "carol", "dave", "erin", "frank"}
	return users[rand.Intn(len(users))]
}

func randomMethodPath() (string, string) {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	paths := []string{"/", "/health", "/login", "/logout", "/api/v1/items", "/api/v1/items/" + fmt.Sprint(randInt(1, 1000)), "/static/app.js", "/static/style.css"}
	return methods[rand.Intn(len(methods))], paths[rand.Intn(len(paths))]
}

func randomProto() string { return "HTTP/1.1" }

func randomStatus() int {
	// Weighted statuses
	r := rand.Float64()
	switch {
	case r < 0.75:
		return 200
	case r < 0.85:
		return 201
	case r < 0.93:
		return 404
	case r < 0.98:
		return 500
	default:
		return 302
	}
}

func randInt(min, max int) int           { return rand.Intn(max-min+1) + min }
func randFloat(min, max float64) float64 { return min + rand.Float64()*(max-min) }

func randomReferer() string {
	refs := []string{"-", "https://example.com/", "https://search.example.com/?q=logs", "https://news.ycombinator.com/", "https://github.com/"}
	return refs[rand.Intn(len(refs))]
}

func randomUserAgent() string {
	agents := []string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:125.0) Gecko/20100101 Firefox/125.0",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"curl/8.2.1",
	}
	return agents[rand.Intn(len(agents))]
}

func randomLevel() string {
	// Weight towards info
	r := rand.Float64()
	switch {
	case r < 0.6:
		return "info"
	case r < 0.8:
		return "debug"
	case r < 0.95:
		return "warn"
	default:
		return "error"
	}
}

func randomMessage() string {
	msgs := []string{
		"user authenticated",
		"request completed",
		"cache miss",
		"cache hit",
		"db query executed",
		"rate limit exceeded",
		"background job started",
		"background job finished",
		"invalid credentials",
		"payload validated",
	}
	return msgs[rand.Intn(len(msgs))]
}

func randomService() string {
	svcs := []string{"api", "worker", "auth", "gateway", "billing"}
	return svcs[rand.Intn(len(svcs))]
}

func randHex(n int) string {
	const hexdigits = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hexdigits[rand.Intn(len(hexdigits))]
	}
	return string(b)
}

func randomEdgeLocation() string {
	edges := []string{"SFO5", "DFW3", "AMS1", "GRU1", "NRT5", "SYD1"}
	return edges[rand.Intn(len(edges))]
}

func randomHost() string {
	hosts := []string{"cdn.example.com", "assets.example.com", "media.example.org", "static.example.net"}
	return hosts[rand.Intn(len(hosts))]
}

func randomQuery() string {
    qs := []string{"-", "q=search", "id=123", "page=2&sort=desc", "utm_source=newsletter"}
    return qs[rand.Intn(len(qs))]
}

// --- Random JSON schema generator ---

type jsonField struct {
    name string
    typ  string // string,int,float,bool,timestamp
}

type jsonSchema struct {
    fields []jsonField
}

func newRandomJSONSchema() jsonSchema {
    // Always include timestamp first for realism
    fields := []jsonField{{name: "ts", typ: "timestamp"}}
    // Candidate field names to sample from
    candidates := []string{
        "level", "service", "msg", "request_id", "user_id", "region", "latency_ms", "ok", "code",
        "path", "method", "host", "retry", "bytes", "session", "feature", "env", "component",
    }
    // Decide total fields (including ts)
    total := randInt(4, 8)
    // Shuffle candidate names
    rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
    pick := 0
    for len(fields) < total && pick < len(candidates) {
        name := candidates[pick]
        pick++
        // Decide type with weights
        t := randomJSONTypeForName(name)
        fields = append(fields, jsonField{name: name, typ: t})
    }
    return jsonSchema{fields: fields}
}

func randomJSONTypeForName(name string) string {
    // Heuristics by name to make plausible types
    switch name {
    case "level", "service", "msg", "request_id", "path", "method", "host", "session", "feature", "env", "component", "region":
        return "string"
    case "user_id", "code", "bytes":
        return "int"
    case "latency_ms":
        return "float"
    case "ok", "retry":
        return "bool"
    default:
        // Weighted fallback
        r := rand.Float64()
        switch {
        case r < 0.5:
            return "string"
        case r < 0.75:
            return "int"
        case r < 0.9:
            return "float"
        default:
            return "bool"
        }
    }
}

func (s jsonSchema) randomRecordJSON() string {
    m := make(map[string]any, len(s.fields))
    now := time.Now().UTC()
    for _, f := range s.fields {
        switch f.typ {
        case "timestamp":
            m[f.name] = now.Format(time.RFC3339Nano)
        case "string":
            m[f.name] = randomStringForField(f.name)
        case "int":
            m[f.name] = randInt(0, 10000)
        case "float":
            m[f.name] = randFloat(0, 5000)
        case "bool":
            m[f.name] = rand.Intn(2) == 0
        default:
            m[f.name] = nil
        }
    }
    b, _ := json.Marshal(m)
    return string(b)
}

func randomStringForField(name string) string {
    switch name {
    case "level":
        return randomLevel()
    case "service":
        return randomService()
    case "msg":
        return randomMessage()
    case "request_id", "session":
        return randHex(16)
    case "path":
        _, p := randomMethodPath()
        return p
    case "method":
        m, _ := randomMethodPath()
        return m
    case "host":
        return randomHost()
    case "feature":
        feats := []string{"search", "checkout", "recommendation", "realtime", "alerts"}
        return feats[rand.Intn(len(feats))]
    case "env":
        envs := []string{"dev", "staging", "prod"}
        return envs[rand.Intn(len(envs))]
    case "component":
        comps := []string{"ingest", "parse", "ui", "export", "auth"}
        return comps[rand.Intn(len(comps))]
    case "region":
        regs := []string{"us-east-1", "us-west-2", "eu-west-1", "sa-east-1", "ap-northeast-1"}
        return regs[rand.Intn(len(regs))]
    default:
        // generic token
        return randHex(8)
    }
}

// --- Text template generator ---

type textTemplate int

func pickTextTemplate() textTemplate {
    return textTemplate(rand.Intn(3))
}

func renderTextTemplate(t textTemplate) string {
    now := time.Now()
    switch t {
    case 0:
        return fmt.Sprintf("[%s] %s %s: %s id=%s", now.UTC().Format(time.RFC3339), randomLevel(), randomService(), randomMessage(), randHex(8))
    case 1:
        method, path := randomMethodPath()
        return fmt.Sprintf("%s %s -> %d user=%s agent=%s", method, path, randomStatus(), randomUser(), randomUserAgent())
    default:
        return fmt.Sprintf("user=%s action=%s ok=%t latency_ms=%.2f", randomUser(), randomAction(), rand.Intn(2) == 0, randFloat(0.5, 450.0))
    }
}

func randomAction() string {
    acts := []string{"login", "logout", "create", "update", "delete", "purchase", "view"}
    return acts[rand.Intn(len(acts))]
}
