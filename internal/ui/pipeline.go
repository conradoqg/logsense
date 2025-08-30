package ui

import (
    "context"
    "sort"
    "strings"
    "time"

    tea "github.com/charmbracelet/bubbletea"

    "logsense/internal/ai"
    "logsense/internal/detect"
    "logsense/internal/ingest"
    "logsense/internal/model"
    "logsense/internal/parse"
    "logsense/internal/util/logx"
)

// IO and pipeline orchestration
func setupPipeline(m *Model) tea.Cmd {
    // Create ingest
    src := ingest.SourceDemo
    if m.cfg.UseStdin {
        src = ingest.SourceStdin
    }
    if !m.cfg.UseStdin && m.cfg.FilePath != "" {
        src = ingest.SourceFile
    }
    m.source = string(src)
    block := int64(0)
    // Use runtime follow state, not only initial config
    if !m.follow && m.cfg.BlockSizeMB > 0 {
        block = int64(m.cfg.BlockSizeMB) * 1024 * 1024
    }
    // If a previous ingest is running, cancel it before starting a new one
    if m.ingestCancel != nil {
        m.ingestCancel()
        m.ingestCancel = nil
    }
    // Create a child context so we can stop this ingest later (e.g., toggling follow)
    ingestCtx, cancel := context.WithCancel(m.ctx)
    m.ingestCancel = cancel
    startOffset := int64(-1)
    if m.follow {
        startOffset = m.tailStartOffset
    }
    m.lines, m.errs = ingest.Read(ingestCtx, ingest.Options{Source: src, Path: m.cfg.FilePath, Follow: m.follow, ScanBufSize: m.scanBufSize, BlockSizeBytes: block, StartOffset: startOffset})
    logx.Infof("ingest: source=%s path=%s follow=%v blockBytes=%d startOffset=%d", m.source, m.cfg.FilePath, m.follow, block, startOffset)
    // Prepare detection: collect first N lines then pick parser
    return func() tea.Msg {
        // Buffer for detection: wait at least 1 second AND at least 1 line.
        // Keep all lines read during this window so nothing is dropped.
        const maxSample = 200
        buffered := make([]ingest.Line, 0, 1024)
        timer := time.NewTimer(1 * time.Second)
        defer timer.Stop()
        haveLine := false
        minElapsed := false
        for !(haveLine && minElapsed) {
            select {
            case l, ok := <-m.lines:
                if !ok {
                    // No more lines; if none were seen, quit; otherwise proceed to detect.
                    if !haveLine {
                        return tea.Quit
                    }
                    minElapsed = true
                    break
                }
                buffered = append(buffered, l)
                haveLine = true
            case <-timer.C:
                minElapsed = true
            case <-m.ctx.Done():
                return tea.Quit
            }
        }
        // Build sample for heuristics from the first lines we buffered (up to maxSample)
        sample := make([]string, 0, maxSample)
        for i := 0; i < len(buffered) && i < maxSample; i++ {
            sample = append(sample, buffered[i].Text)
        }
        // Heuristics
        g := detect.Heuristics(sample)
        m.schema = g.Schema
        logx.Infof("detect: heuristics format=%s strategy=%s conf=%.2f", m.schema.FormatName, m.schema.ParseStrategy, g.Confidence)
        if m.cfg.ForceFormat != "" {
            switch m.cfg.ForceFormat {
            case "json":
                m.schema.ParseStrategy = "json"
            case "logfmt":
                m.schema.ParseStrategy = "logfmt"
            case "apache":
                m.schema = detect.Heuristics([]string{"127.0.0.1 - - [01/Jan/2025:12:00:02 +0000] \"GET / HTTP/1.1\" 200 1234 \"-\" \"curl/8.0\""}).Schema
            case "syslog":
                m.schema = detect.Heuristics([]string{"<34>1 2025-01-01T00:00:00Z h a - - - msg"}).Schema
            }
            logx.Infof("detect: forced format=%s -> strategy=%s", m.cfg.ForceFormat, m.schema.ParseStrategy)
        }
        // If online and a file path is provided, try schema cache before creating parser
        cacheHit := false
        if !m.cfg.NoCache && strings.TrimSpace(m.cfg.FilePath) != "" {
            if cs, ok := detect.LoadSchemaFromCache(m.cfg.FilePath); ok {
                m.schema = cs
                cacheHit = true
                logx.Infof("detect: cache hit for %s -> format=%s strategy=%s", m.cfg.FilePath, m.schema.FormatName, m.schema.ParseStrategy)
            } else {
                logx.Infof("detect: no cache for %s", m.cfg.FilePath)
            }
        } else if m.cfg.NoCache {
            logx.Infof("detect: cache disabled via --no-cache")
        }
        p, _ := parse.NewParser(m.schema, m.cfg.TimeLayout)
        m.parser = p
        // Replay ALL buffered lines so none are lost and infer columns from parsed fields
        fieldSet := map[string]struct{}{}
        var sampleRow map[string]any
        for _, bl := range buffered {
            e := p.Parse(bl.Text, bl.Source)
            if sampleRow == nil {
                sampleRow = e.Fields
            }
            for k := range e.Fields {
                if strings.TrimSpace(k) == "" {
                    continue
                }
                fieldSet[k] = struct{}{}
            }
            m.ring.Push(e)
            if m.updateDiscoveryFromEntry(e) {
                m.columnsDirty = true
            }
            m.rowsDirty = true
        }
        if len(fieldSet) > 0 {
            keys := make([]string, 0, len(fieldSet))
            for k := range fieldSet {
                keys = append(keys, k)
            }
            sort.Strings(keys)
            fdefs := make([]model.FieldDef, 0, len(keys))
            for _, k := range keys {
                fdefs = append(fdefs, model.FieldDef{Name: k, Type: "string", Description: "", PathOrGroup: k})
            }
            m.schema.Fields = fdefs
            if sampleRow != nil {
                m.schema.SampleParsedRow = sampleRow
            }
        }
        // If OpenAI is configured and we did not have a cache hit, start async inference and return detectedMsg immediately
        if !m.cfg.Offline && m.cfg.OpenAIKey() != "" && !cacheHit {
            client := ai.NewOpenAIClient(m.cfg.OpenAIKey(), m.cfg.OpenAIBase, m.cfg.OpenAIModel, time.Duration(m.cfg.OpenAITimeoutSec)*time.Second)
            // Use up to 50 lines of buffered sample for inference
            max := 50
            if len(sample) < max {
                max = len(sample)
            }
            lines := sample[:max]
            return tea.Batch(
                func() tea.Msg { return detectedMsg{} },
                func() tea.Msg { return openaiStartMsg{} },
                func() tea.Msg {
                    ctx, cancel := context.WithTimeout(m.ctx, time.Duration(m.cfg.OpenAITimeoutSec)*time.Second)
                    defer cancel()
                    s, err := client.InferSchema(ctx, lines)
                    if err != nil {
                        // Preserve error details for app logs and status
                        return openaiDoneMsg{ok: false, err: err.Error()}
                    }
                    return openaiDoneMsg{ok: true, schema: s}
                },
            )
        }
        return detectedMsg{}
    }
}

type detectedMsg struct{}
type tickMsg struct{}
type redetectMsg struct{ schema model.Schema }
type openaiStartMsg struct{}
type openaiDoneMsg struct {
    ok     bool
    schema model.Schema
    err    string
}

// Simple UI toast/status message
type toastMsg struct{ text string }
type loadDoneMsg struct{}

func (m *Model) triggerRedetect() tea.Cmd {
    // Collect last lines
    entries, _, _ := m.ring.Snapshot()
    n := len(entries)
    if n == 0 {
        msg := "no data yet to detect; wait for load to finish"
        logx.Warnf("redetect: %s", msg)
        return func() tea.Msg { return toastMsg{text: msg} }
    }
    start := n - 50
    if start < 0 {
        start = 0
    }
    lines := make([]string, 0, 50)
    for i := start; i < n && len(lines) < 50; i++ {
        lines = append(lines, entries[i].Raw)
    }
    g := detect.Heuristics(lines[:min(10, len(lines))])
    logx.Infof("redetect: heuristics format=%s strategy=%s conf=%.2f (lines=%d)", g.Schema.FormatName, g.Schema.ParseStrategy, g.Confidence, len(lines))
    // If online and API key is set, always try OpenAI during re-detect
    if !m.cfg.Offline && m.cfg.OpenAIKey() != "" {
        client := ai.NewOpenAIClient(m.cfg.OpenAIKey(), m.cfg.OpenAIBase, m.cfg.OpenAIModel, time.Duration(m.cfg.OpenAITimeoutSec)*time.Second)
        return tea.Batch(
            func() tea.Msg { return openaiStartMsg{} },
            func() tea.Msg {
                ctx, cancel := context.WithTimeout(m.ctx, time.Duration(m.cfg.OpenAITimeoutSec)*time.Second)
                defer cancel()
                s, err := client.InferSchema(ctx, lines[:min(10, len(lines))])
                if err != nil {
                    return openaiDoneMsg{ok: false, err: err.Error()}
                }
                return openaiDoneMsg{ok: true, schema: s}
            },
        )
    }
    logx.Infof("redetect: applying heuristics schema")
    return func() tea.Msg { return redetectMsg{schema: g.Schema} }
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
