package ui

import (
    "context"
    "encoding/json"
    "fmt"
    "sort"
    "strings"
    "time"
    "regexp"

    "github.com/charmbracelet/bubbles/help"
    "github.com/charmbracelet/bubbles/spinner"
    "github.com/charmbracelet/bubbles/table"
    "github.com/charmbracelet/bubbles/textinput"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"

    "logsense/internal/config"
    "logsense/internal/detect"
    "logsense/internal/export"
    "logsense/internal/filter"
    "logsense/internal/ingest"
    "logsense/internal/model"
    "logsense/internal/parse"
)

type tab int

const (
	tabStream tab = iota
	tabFilters
	tabInspector
	tabHelp
)

type state int

const (
	stateRunning state = iota
	statePaused
)

type Model struct {
	ctx context.Context
	cfg *config.Config

	// Pipeline
	lines  <-chan ingest.Line
	errs   <-chan error
	parser parse.Parser
	schema model.Schema

	// Data
	ring     *model.Ring
	filtered []model.LogEntry
	total    uint64
	dropped  uint64

	// UI
	tab      tab
	state    state
	tbl      table.Model
	help     help.Model
	styles   Styles
	search   textinput.Model
	viewport viewport.Model
	spin     spinner.Model
    keymap   KeyMap
    cols     []string
    colOffset int
    maxCols   int
    selColIdx int // index in full column list
    termWidth  int
    termHeight int

	// Filter
	criteria filter.Criteria
	eval     *filter.Evaluator

	// status
	source     string
	follow     bool
	lastMsg    string
	lastSearch string
	showHelp   bool
	showStats  bool
	statsField string
    netBusy    bool
    failStreak int

    // Modal popup
    modalActive bool
    modalKind   modalKind
    modalVP     viewport.Model
    modalTitle  string
    modalBody   string

    // Search state (navigation)
    searchActive  bool
    searchPattern string
    searchRegex   bool

    // Inline input mode (instead of modal for search/filter)
    inlineMode inlineMode
    searchEditing bool
}

type modalKind int

const (
    modalNone modalKind = iota
    modalHelp
    modalStats
    modalInspector
    modalSearch
    modalFilter
)

type inlineMode int

const (
    inlineNone inlineMode = iota
    inlineSearch
    inlineFilter
)

func initialModel(ctx context.Context, cfg *config.Config) *Model {
	m := &Model{
		ctx:    ctx,
		cfg:    cfg,
		ring:   model.NewRing(cfg.MaxBuffer),
		tab:    tabStream,
		state:  stateRunning,
		help:   help.New(),
		styles: NewStyles(cfg.Theme == config.ThemeDark),
		keymap: DefaultKeyMap(),
		search: textinput.New(),
		spin:   spinner.New(),
		follow: cfg.Follow,
	}
	m.spin.Spinner = spinner.Dot
	m.search.Placeholder = "search... (text or /regex/)"
	m.search.CharLimit = 256
	m.search.Prompt = "/"
	m.viewport = viewport.New(80, 20)

    m.tbl = table.New(table.WithFocused(true), table.WithHeight(20))
    m.tbl.SetColumns([]table.Column{{Title: "ts", Width: 19}, {Title: "level", Width: 6}, {Title: "source", Width: 12}, {Title: "message", Width: 80}})
    // Remove default padding to make width math exact
    ts := table.DefaultStyles()
    ts.Header = lipgloss.NewStyle()
    ts.Cell = lipgloss.NewStyle()
    ts.Selected = m.styles.TableStyles.Selected
    m.tbl.SetStyles(ts)
    m.maxCols = 6
    m.selColIdx = 0
	return m
}

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
	if !m.cfg.Follow && m.cfg.BlockSizeMB > 0 {
		block = int64(m.cfg.BlockSizeMB) * 1024 * 1024
	}
	m.lines, m.errs = ingest.Read(m.ctx, ingest.Options{Source: src, Path: m.cfg.FilePath, Follow: m.cfg.Follow, ScanBufSize: 1024 * 1024, BlockSizeBytes: block})
	// Prepare detection: collect first N lines then pick parser
    return func() tea.Msg {
        sample := make([]string, 0, 10)
        timeout := time.After(1200 * time.Millisecond)
        for len(sample) < 10 {
			select {
			case l, ok := <-m.lines:
				if !ok {
					break
				}
				sample = append(sample, l.Text)
				if len(sample) >= 10 { // quick detection
					goto DETECT
				}
			case <-timeout:
				goto DETECT
			case <-m.ctx.Done():
				return tea.Quit
			}
		}
    DETECT:
        // Heuristics
        g := detect.Heuristics(sample)
        m.schema = g.Schema
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
        }
        p, _ := parse.NewParser(m.schema, m.cfg.TimeLayout)
        m.parser = p
        // Replay sampled lines so small files are not lost and infer columns from parsed fields
        fieldSet := map[string]struct{}{}
        var sampleRow map[string]any
        for _, s := range sample {
            e := p.Parse(s, m.source)
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
}
type loadDoneMsg struct{}

// paging removed; loadMoreDoneMsg no longer used

func (m *Model) triggerRedetect() tea.Cmd {
	// Collect last lines
	entries, _, _ := m.ring.Snapshot()
	n := len(entries)
	if n == 0 {
		return nil
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
	// If heuristics not confident and online, try OpenAI
	if !m.cfg.Offline && m.cfg.OpenAIKey() != "" && (g.Schema.FormatName == "unknown" || g.Confidence < 0.5) {
		client := detect.NewOpenAIClient(m.cfg.OpenAIKey(), m.cfg.OpenAIBase, m.cfg.OpenAIModel, 20*time.Second)
		return tea.Batch(
			func() tea.Msg { return openaiStartMsg{} },
			func() tea.Msg {
				ctx, cancel := context.WithTimeout(m.ctx, 25*time.Second)
				defer cancel()
				s, err := client.InferSchema(ctx, lines[:min(10, len(lines))])
				if err != nil {
					return openaiDoneMsg{ok: false}
				}
				return openaiDoneMsg{ok: true, schema: s}
			},
		)
	}
	return func() tea.Msg { return redetectMsg{schema: g.Schema} }
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func Run(ctx context.Context, cfg *config.Config) error {
	m := initialModel(ctx, cfg)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(setupPipeline(m), tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} }))
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        m.termWidth, m.termHeight = msg.Width, msg.Height
        // Fit table height (leave room for status/help overlays)
        h := msg.Height - 3
        if h < 3 { h = 3 }
        m.tbl.SetHeight(h)
        m.tbl.SetWidth(msg.Width)
        m.autofitMaxCols()
        m.applyColumns(m.visibleColumns(m.deriveColumns()))
        m.refreshFiltered()
        if m.modalActive {
            m.resizeModal()
        }
        return m, nil
    case tea.KeyMsg:
        if m.modalActive {
            // Modal key handling
            if msg.Type == tea.KeyEsc || msg.Type == tea.KeyEnter {
                // If applying in search/filter
                if msg.Type == tea.KeyEnter {
                    if m.modalKind == modalSearch {
                        q := strings.TrimSpace(m.search.Value())
                        if q != "" {
                            m.searchActive = true
                            if strings.HasPrefix(q, "/") && strings.HasSuffix(q, "/") && len(q) > 2 {
                                m.searchRegex = true
                                m.searchPattern = q[1:len(q)-1]
                            } else {
                                m.searchRegex = false
                                m.searchPattern = q
                            }
                            m.searchNext()
                        } else {
                            m.searchActive = false
                            m.searchPattern = ""
                        }
                    }
                    if m.modalKind == modalFilter {
                        q := strings.TrimSpace(m.search.Value())
                        if q != "" {
                            // Apply criteria on selected column
                            m.criteria.Query = q
                            m.criteria.UseRegex = strings.HasPrefix(q, "/") && strings.HasSuffix(q, "/") && len(q) > 2
                            if m.criteria.UseRegex { m.criteria.Query = q[1:len(q)-1] }
                            all := m.deriveColumns()
                            if len(all) > 0 {
                                if m.selColIdx >= len(all) { m.selColIdx = len(all)-1 }
                                m.criteria.Field = all[m.selColIdx]
                            }
                            if ev, err := filter.NewEvaluator(m.criteria); err == nil { m.eval = ev }
                            m.refreshFiltered()
                        } else {
                            m.criteria.Query = ""
                            m.criteria.Field = ""
                            if ev, err := filter.NewEvaluator(m.criteria); err == nil { m.eval = ev }
                            m.refreshFiltered()
                        }
                    }
                }
                m.modalActive = false
                return m, nil
            }
            if msg.Type == tea.KeyRunes && (msg.String() == "C" || msg.String() == "c") && (m.modalKind == modalInspector || m.modalKind == modalStats) {
                copyToClipboard(m.modalBody)
                m.lastMsg = "copied to clipboard"
                return m, nil
            }
            // Route typing to text input when search/filter modal is active
            if m.modalKind == modalSearch || m.modalKind == modalFilter {
                var cmd tea.Cmd
                m.search, cmd = m.search.Update(msg)
                return m, cmd
            }
            // Otherwise, scroll modal viewport
            var cmd tea.Cmd
            m.modalVP, cmd = m.modalVP.Update(msg)
            return m, cmd
        }
        // Inline input handling for search/filter (bottom line)
        if m.inlineMode == inlineSearch || m.inlineMode == inlineFilter {
            // Enter applies; Esc cancels
            if msg.Type == tea.KeyEnter {
                q := strings.TrimSpace(m.search.Value())
                if m.inlineMode == inlineSearch {
                    if m.searchEditing {
                        if q != "" {
                            m.searchActive = true
                            if strings.HasPrefix(q, "/") && strings.HasSuffix(q, "/") && len(q) > 2 {
                                m.searchRegex = true
                                m.searchPattern = q[1:len(q)-1]
                            } else {
                                m.searchRegex = false
                                m.searchPattern = q
                            }
                            m.searchNext()
                            m.ensureCursorVisible()
                        }
                        // Switch to read-only navigation within search mode
                        m.searchEditing = false
                        return m, nil
                    }
                    // If already in read-only search mode, Enter toggles back to editing
                    m.searchEditing = true
                    m.search.Focus()
                    return m, nil
                } else if m.inlineMode == inlineFilter {
                    if q != "" {
                        m.criteria.Query = q
                        m.criteria.UseRegex = strings.HasPrefix(q, "/") && strings.HasSuffix(q, "/") && len(q) > 2
                        if m.criteria.UseRegex { m.criteria.Query = q[1:len(q)-1] }
                        all := m.deriveColumns()
                        if len(all) > 0 {
                            if m.selColIdx >= len(all) { m.selColIdx = len(all)-1 }
                            m.criteria.Field = all[m.selColIdx]
                        }
                        if ev, err := filter.NewEvaluator(m.criteria); err == nil { m.eval = ev }
                        m.refreshFiltered()
                        m.ensureCursorVisible()
                    }
                    // Exit filter mode after applying
                    m.inlineMode = inlineNone
                    return m, nil
                }
                return m, nil
            }
            if msg.Type == tea.KeyEsc {
                m.inlineMode = inlineNone
                m.searchEditing = false
                // Deactivate search outside of search mode
                m.searchActive = false
                m.searchPattern = ""
                m.searchRegex = false
                return m, nil
            }
            // While in search mode and read-only, handle only n/N shortcuts
            if m.inlineMode == inlineSearch && !m.searchEditing {
                if keyMatches(msg, m.keymap.SearchNext) {
                    m.searchNext()
                    return m, nil
                }
                if keyMatches(msg, m.keymap.SearchPrev) {
                    m.searchPrev()
                    return m, nil
                }
                // Do not swallow other keys; allow table/shortcuts to work
            } else if m.inlineMode == inlineSearch && m.searchEditing {
                // Route only text-editing keys to the input; pass others through
                if msg.Type == tea.KeyRunes || msg.Type == tea.KeyBackspace || msg.Type == tea.KeyDelete {
                    var cmd tea.Cmd
                    m.search, cmd = m.search.Update(msg)
                    return m, cmd
                }
                // fallthrough for non-editing keys
            } else if m.inlineMode == inlineFilter {
                // For filter input, capture typing fully
                var cmd tea.Cmd
                m.search, cmd = m.search.Update(msg)
                return m, cmd
            }
            // fallthrough: let general handlers process
        }
        switch {
        case msg.Type == tea.KeyCtrlC:
            return m, tea.Quit
        case keyMatches(msg, m.keymap.Filter):
            // Open inline filter input on selected column
            m.search.Prompt = "f> "
            m.search.Placeholder = "filter (text or /regex/)"
            m.search.SetValue("")
            m.search.Focus()
            m.inlineMode = inlineFilter
            return m, nil
        case keyMatches(msg, m.keymap.Pause):
			if m.state == stateRunning {
				m.state = statePaused
			} else {
				m.state = stateRunning
			}
		case keyMatches(msg, m.keymap.Clear):
			m.ring.ClearVisible()
			m.filtered = nil
			m.total, m.dropped = 0, 0
		case keyMatches(msg, m.keymap.Follow):
			m.follow = !m.follow
			m.lastMsg = fmt.Sprintf("follow: %v", m.follow)
		case keyMatches(msg, m.keymap.Export):
			if m.cfg.ExportFormat != "" && m.cfg.ExportOut != "" {
				go func() {
					switch m.cfg.ExportFormat {
					case "csv":
						_ = export.ToCSV(m.cfg.ExportOut, m.filtered)
					case "json":
						_ = export.ToNDJSON(m.cfg.ExportOut, m.filtered)
					}
				}()
				m.lastMsg = fmt.Sprintf("exported %d rows to %s (%s)", len(m.filtered), m.cfg.ExportOut, m.cfg.ExportFormat)
			} else {
				m.lastMsg = "use --export and --out to export"
			}
        case keyMatches(msg, m.keymap.Help):
            m.openHelpModal()
		case keyMatches(msg, m.keymap.StreamTab):
			m.tab = tabStream
		case keyMatches(msg, m.keymap.FilterTab):
			m.tab = tabFilters
        case keyMatches(msg, m.keymap.InspectorTab):
            m.openInspectorModal()
        case keyMatches(msg, m.keymap.Stats):
            // Open stats modal for selected column
            all := m.deriveColumns()
            if len(all) > 0 {
                if m.selColIdx >= len(all) { m.selColIdx = len(all)-1 }
                m.statsField = all[m.selColIdx]
            }
            m.openStatsModal()
        case keyMatches(msg, m.keymap.NextStats):
            // Set stats to selected column (no cycling)
            all := m.deriveColumns()
            if len(all) > 0 {
                if m.selColIdx >= len(all) { m.selColIdx = len(all)-1 }
                m.statsField = all[m.selColIdx]
                m.openStatsModal()
            }
    case msg.Type == tea.KeyLeft:
        if m.selColIdx > 0 {
            m.selColIdx--
            if m.selColIdx < m.colOffset {
                if m.colOffset > 0 { m.colOffset-- }
            }
            m.refreshFiltered()
        }
    case msg.Type == tea.KeyRight:
        all := m.deriveColumns()
        if m.selColIdx+1 < len(all) {
            m.selColIdx++
            if m.selColIdx >= m.colOffset+m.maxCols {
                m.colOffset++
            }
            m.refreshFiltered()
        }
		case keyMatches(msg, m.keymap.Top):
			m.tbl.SetCursor(0)
		case keyMatches(msg, m.keymap.Bottom):
			if n := len(m.tbl.Rows()); n > 0 {
				m.tbl.SetCursor(n - 1)
			}
		case keyMatches(msg, m.keymap.Summarize):
			m.lastMsg = "Summarize (OpenAI) unavailable in offline mode"
		case keyMatches(msg, m.keymap.Explain):
			m.lastMsg = "Explain (OpenAI) unavailable in offline mode"
        case keyMatches(msg, m.keymap.Redetect):
            return m, m.triggerRedetect()
            // load more removed after simplifying to full-file reads
        case keyMatches(msg, m.keymap.Search):
            m.search.Prompt = "/"
            m.search.Placeholder = "search (text or /regex/)"
            m.search.SetValue("")
            m.search.Focus()
            m.inlineMode = inlineSearch
            m.searchEditing = true
            return m, nil
        case keyMatches(msg, m.keymap.SearchNext):
            if m.inlineMode == inlineSearch {
                m.searchNext()
                return m, nil
            }
        case keyMatches(msg, m.keymap.SearchPrev):
            if m.inlineMode == inlineSearch {
                m.searchPrev()
                return m, nil
            }
        case keyMatches(msg, m.keymap.ClearFilter):
            if m.criteria.Query != "" || m.criteria.Field != "" {
                m.criteria.Query = ""
                m.criteria.Field = ""
                if ev, err := filter.NewEvaluator(m.criteria); err == nil { m.eval = ev }
                m.refreshFiltered()
                m.lastMsg = "filter cleared"
            }
        case keyMatches(msg, m.keymap.CopyLine):
            idx := m.tbl.Cursor()
            if idx >= 0 && idx < len(m.filtered) {
                copyToClipboard(m.filtered[idx].Raw)
                m.lastMsg = "copied line to clipboard"
            }
        case keyMatches(msg, m.keymap.Quit):
            return m, tea.Quit
        }
    case detectedMsg:
        // For non-follow file reads, drain asynchronously with spinner
        if m.parser != nil && m.source == string(ingest.SourceFile) && !m.follow {
            m.netBusy = true
            m.lastMsg = "📥 Loading file..."
            drain := func() tea.Msg {
                for l := range m.lines {
                    e := m.parser.Parse(l.Text, l.Source)
                    m.ring.Push(e)
                }
                return loadDoneMsg{}
            }
            // Set columns from detected schema and render immediately; start async drain
            m.applyColumns(m.visibleColumns(m.deriveColumns()))
            m.refreshFiltered()
            if n := len(m.tbl.Rows()); n > 0 { m.tbl.SetCursor(n - 1) }
            return m, drain
        }
        // Set columns from detected schema and render immediately
        // Initialize selected column to msg/message or first
        all := m.deriveColumns()
        m.selColIdx = 0
        for i, c := range all {
            if c == "msg" || c == "message" { m.selColIdx = i; break }
        }
        // Ensure selected column is visible
        if m.selColIdx >= m.colOffset+m.maxCols {
            m.colOffset = m.selColIdx - (m.maxCols - 1)
            if m.colOffset < 0 { m.colOffset = 0 }
        }
        m.applyColumns(m.visibleColumns(all))
        m.refreshFiltered()
        if n := len(m.tbl.Rows()); n > 0 {
            m.tbl.SetCursor(n - 1)
        }
        // loadMoreDoneMsg removed
    case loadDoneMsg:
        m.netBusy = false
        m.lastMsg = ""
        m.refreshFiltered()
	case openaiStartMsg:
		m.netBusy = true
		m.lastMsg = "📡 OpenAI: inferring schema..."
	case openaiDoneMsg:
		m.netBusy = false
		if msg.ok {
			m.schema = msg.schema
			p, _ := parse.NewParser(m.schema, m.cfg.TimeLayout)
			m.parser = p
			m.lastMsg = fmt.Sprintf("✅ OpenAI schema: %s (%.0f%%)", m.schema.FormatName, m.schema.Confidence*100)
		} else {
			m.lastMsg = "⚠️ OpenAI failed; keeping heuristics"
		}
	case tickMsg:
		// Pull lines non-blocking, parse, push to ring
		if m.state == stateRunning {
			for i := 0; i < 500; i++ { // limit per tick
				select {
				case l, ok := <-m.lines:
					if !ok {
						break
					}
					if m.parser != nil {
						e := m.parser.Parse(l.Text, l.Source)
						m.ring.Push(e)
						if !parsedOK(e) {
							m.failStreak++
						} else {
							m.failStreak = 0
						}
						if m.failStreak >= 20 {
							m.failStreak = 0
							return m, m.triggerRedetect()
						}
					}
				default:
					i = 999999 // break outer
				}
			}
		}
		m.refreshFiltered()
		return m, tea.Tick(200*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
	}

	var cmd tea.Cmd
	m.tbl, cmd = m.tbl.Update(msg)
	// Clamp cursor to avoid wrap-around behavior
	if n := len(m.tbl.Rows()); n > 0 {
		if m.tbl.Cursor() < 0 {
			m.tbl.SetCursor(0)
		}
		if m.tbl.Cursor() >= n {
			m.tbl.SetCursor(n - 1)
		}
	}
	m.search, _ = m.search.Update(msg)
	m.viewport, _ = m.viewport.Update(msg)
	m.spin, _ = m.spin.Update(msg)
	// Virtual paging removed; no autoload/adjustment
	return m, cmd
}

func (m *Model) View() string {
    switch m.tab {
    case tabStream:
        v := m.renderStream()
        if m.modalActive {
            v = overlay(v, m.renderModal())
        }
        return v
    case tabFilters:
        return m.renderFilters()
    case tabInspector:
        return m.renderInspector()
	default:
		return m.renderHelp()
	}
}

func (m *Model) refreshFiltered() {
    // Only apply field-scoped filter via m.criteria (set when applying filter)
    // Do not derive filtering from the search input; search is navigational only.
    if ev, err := filter.NewEvaluator(m.criteria); err == nil {
        m.eval = ev
    }

    rows := []table.Row{}
    entries, total, dropped := m.ring.Snapshot()
	m.total, m.dropped = total, dropped
	m.filtered = m.filtered[:0]
    allCols := m.deriveColumns()
    cols := m.visibleColumns(allCols)

	for i := range entries {
		e := entries[i]
		if m.eval != nil && !m.eval.Match(e, m.criteria) {
			continue
		}
		m.filtered = append(m.filtered, e)
	}
    for i := range m.filtered {
        e := m.filtered[i]
        row := make([]string, 0, len(cols))
        for j, c := range cols {
            cell := getCol(e, c)
            if j > 0 {
                cell = " " + cell
            }
            row = append(row, cell)
        }
        rows = append(rows, row)
    }
    m.applyColumns(cols)
    m.tbl.SetRows(rows)
    // Keep current selection visible on refresh
    m.ensureCursorVisible()
}

func (m *Model) deriveColumns() []string {
	// Use only the schema-defined order to reflect the log schema
	cols := m.schema.ColumnOrder()
	if len(cols) == 0 {
		cols = []string{"ts", "level", "source", "msg", "message"}
	}
    return cols
}

func (m *Model) visibleColumns(all []string) []string {
    if len(all) == 0 {
        return all
    }
    if m.maxCols <= 0 {
        m.autofitMaxCols()
        if m.maxCols <= 0 { m.maxCols = 6 }
    }
    if m.colOffset < 0 {
        m.colOffset = 0
    }
    if m.colOffset >= len(all) {
        if len(all) > m.maxCols {
            m.colOffset = len(all) - m.maxCols
        } else {
            m.colOffset = 0
        }
    }
    end := m.colOffset + m.maxCols
    if end > len(all) {
        end = len(all)
    }
    return all[m.colOffset:end]
}

// Estimate how many columns fit in current terminal width.
func (m *Model) autofitMaxCols() {
    all := m.deriveColumns()
    if len(all) == 0 { m.maxCols = 0; return }
    width := m.termWidth
    if width <= 0 { width = 120 } // default before first WindowSizeMsg
    // Account for minimal padding and separators (~3 chars per column)
    avail := width - 4
    if avail < 20 { avail = 20 }
    sum := 0
    count := 0
    for i := m.colOffset; i < len(all); i++ {
        w := m.columnWidth(all[i]) + 3
        if count == 0 || sum+w <= avail {
            sum += w
            count++
        } else {
            break
        }
    }
    if count <= 0 { count = 1 }
    m.maxCols = count
}

func (m *Model) columnWidth(c string) int {
    switch c {
    case "ts", "time", "timestamp":
        return 19
    case "level", "lvl", "severity":
        return 6
    case "msg", "message":
        return 60
    default:
        return 12
    }
}

func (m *Model) nextStatsField() string {
	cols := m.cols
	if len(cols) == 0 {
		cols = m.schema.ColumnOrder()
	}
	if len(cols) == 0 {
		return "msg"
	}
	cur := m.statsField
	// Cycle through non-preferred first, then preferred, skipping duplicates
	pref := map[string]bool{"ts": true, "time": true, "timestamp": true, "level": true, "lvl": true, "severity": true, "source": true, "component": true}
	list := []string{}
	for _, c := range cols {
		if !pref[c] {
			list = append(list, c)
		}
	}
	for _, c := range cols {
		if pref[c] {
			list = append(list, c)
		}
	}
	if len(list) == 0 {
		return cols[0]
	}
	idx := 0
	for i, c := range list {
		if c == cur {
			idx = i
			break
		}
	}
	idx = (idx + 1) % len(list)
	return list[idx]
}

func (m *Model) searchNext() {
    if !m.searchActive || m.searchPattern == "" { return }
    start := m.tbl.Cursor() + 1
    for i := 0; i < len(m.filtered); i++ {
        idx := (start + i) % len(m.filtered)
        if m.entryMatchesSearch(m.filtered[idx]) {
            m.tbl.SetCursor(idx)
            m.ensureCursorVisible()
            return
        }
    }
}

func (m *Model) searchPrev() {
    if !m.searchActive || m.searchPattern == "" { return }
    start := m.tbl.Cursor() - 1
    if start < 0 { start = len(m.filtered) - 1 }
    for i := 0; i < len(m.filtered); i++ {
        idx := start - i
        if idx < 0 { idx += len(m.filtered) }
        if m.entryMatchesSearch(m.filtered[idx]) {
            m.tbl.SetCursor(idx)
            m.ensureCursorVisible()
            return
        }
    }
}

func (m *Model) ensureCursorVisible() {
    // Nudge the internal viewport so the cursor row is visible
    cur := m.tbl.Cursor()
    rows := m.tbl.Rows()
    if len(rows) == 0 { return }
    if cur < len(rows)-1 {
        m.tbl.MoveDown(1)
        m.tbl.MoveUp(1)
        return
    }
    if cur > 0 {
        m.tbl.MoveUp(1)
        m.tbl.MoveDown(1)
        return
    }
    m.tbl.SetCursor(cur)
}

func (m *Model) entryMatchesSearch(e model.LogEntry) bool {
    text := e.Raw
    if m.searchRegex {
        re, err := regexp.Compile(m.searchPattern)
        if err != nil { return false }
        return re.MatchString(text)
    }
    return strings.Contains(strings.ToLower(text), strings.ToLower(m.searchPattern))
}

func getCol(e model.LogEntry, c string) string {
    switch c {
    case "ts", "time", "timestamp":
        if e.Timestamp != nil {
            return e.Timestamp.Format("2006-01-02 15:04:05")
        }
        if v, ok := e.Fields[c]; ok {
            return anyToString(v)
        }
    case "level", "lvl", "severity":
        return e.Level
    case "source", "component":
        if e.Source != "" {
            return e.Source
        }
        if v, ok := e.Fields[c]; ok {
            return anyToString(v)
        }
    case "msg", "message":
        if v, ok := e.Fields[c]; ok {
            return anyToString(v)
        }
    }
    // For any non-special column, if the field exists, return it.
    if v, ok := e.Fields[c]; ok {
        return anyToString(v)
    }
    // Fallback first string field
    keys := make([]string, 0, len(e.Fields))
    for k := range e.Fields {
        keys = append(keys, k)
    }
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := e.Fields[k]; ok {
			return anyToString(v)
		}
	}
	return e.Raw
}

func anyToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64, float32, int, int32, int64, uint, uint32, uint64, bool:
		return fmt.Sprint(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func (m *Model) applyColumns(cols []string) {
    // Compute column widths to fit terminal
    widths := m.computeWidths(cols)
    cs := make([]table.Column, 0, len(cols))
    for i, c := range cols {
        title := c
        if i > 0 { title = " " + title }
        cs = append(cs, table.Column{Title: title, Width: widths[i]})
    }
    m.cols = cols
    m.tbl.SetColumns(cs)
}

// computeWidths returns a width per column that fits terminal width.
func (m *Model) computeWidths(cols []string) []int {
    if len(cols) == 0 {
        return nil
    }
    base := make([]int, len(cols))
    sum := 0
    for i, c := range cols {
        w := m.columnWidth(c)
        // add 1 space separator except for last column
        if i < len(cols)-1 {
            w += 1
        }
        base[i] = w
        sum += w
    }
    avail := m.termWidth
    if avail <= 0 { avail = 120 }
    sep := (len(cols) - 1) * 2 // approximate separators
    extra := avail - (sum + sep)
    if extra != 0 {
        idx := len(cols) - 1
        for i, c := range cols {
            if c == "msg" || c == "message" { idx = i; break }
        }
        if base[idx]+extra < 8 {
            base[idx] = 8
        } else {
            base[idx] += extra
        }
    }
    return base
}

func (m *Model) renderStream() string {
    busy := ""
    if m.netBusy {
        busy = " " + m.spin.View()
    }
    // Build underline indicator for selected column under the header
    // Insert it right after the header line of the table
    tv := m.tbl.View()
    underline := m.renderUnderline()
    // splice underline after the first newline in table view
    hEnd := strings.IndexByte(tv, '\n')
    if hEnd > -1 {
        tv = tv[:hEnd+1] + underline + "\n" + tv[hEnd+1:]
    } else {
        tv = tv + "\n" + underline
    }
    // Build dynamic hint/status trail
    hint := "  [?]=help arrows: rows/cols  x/X: stats"
    // Do not show search shortcuts in the general status bar; they appear in the sub-statusbar when in search mode
    // Filter hints
    if m.inlineMode == inlineFilter {
        hint += "  [Enter]=apply [Esc]=cancel [F]=clear filter"
    } else if m.criteria.Query != "" {
        hint += "  [F]=clear filter"
    }
    // Current cursor position among filtered rows
    cur := m.tbl.Cursor()
    if cur < 0 { cur = -1 }
    // Make it 1-based for display; clamp at 0 when no rows
    curDisp := 0
    total := len(m.filtered)
    if cur >= 0 && total > 0 {
        if cur >= total { cur = total - 1 }
        curDisp = cur + 1
    }
    // Show rows (visible) and ingested counters to avoid confusion
    status := fmt.Sprintf("[%s] line:%d/%d rows:%d ingested:%d dropped:%d format:%s follow:%v source:%s%s  %s%s",
        map[state]string{stateRunning: "Running", statePaused: "Paused"}[m.state],
        curDisp, total,
        len(m.filtered), m.total, m.dropped, m.schema.FormatName, m.follow, m.source, hint, m.lastMsg, busy)
    // Inline input line above status bar
    var bottom string
    if m.inlineMode == inlineSearch {
        // Show current term and shortcuts; stays until ESC (vim-like)
        term := m.search.Value()
        if m.searchEditing {
            bottom = fmt.Sprintf("search: %s    [Enter]=apply [Esc]=quit mode [n/N]=next/prev", term)
        } else {
            // Read-only navigation: n/N work; Enter toggles back to edit
            disp := m.searchPattern
            if disp == "" { disp = term }
            bottom = fmt.Sprintf("search: %s    [Enter]=edit [Esc]=quit mode [n/N]=next/prev", disp)
        }
    } else if m.inlineMode == inlineFilter {
        bottom = fmt.Sprintf("Filter %s: %s", m.currentColumn(), m.search.View())
    }
    if bottom != "" {
        return lipgloss.JoinVertical(lipgloss.Left, tv, bottom, m.styles.Status.Render(status))
    }
    return lipgloss.JoinVertical(lipgloss.Left, tv, m.styles.Status.Render(status))
}

// renderUnderline renders a visual underline under the selected column header.
func (m *Model) renderUnderline() string {
    cols := m.cols
    if len(cols) == 0 {
        cols = m.visibleColumns(m.deriveColumns())
    }
    if len(cols) == 0 {
        return ""
    }
    widths := m.computeWidths(cols)
    // Determine selected index within visible window
    visStart := m.colOffset
    visEnd := m.colOffset + len(cols)
    if m.selColIdx < visStart || m.selColIdx >= visEnd {
        // selected column not visible
        if m.termWidth <= 0 { return "" }
        return strings.Repeat(" ", m.termWidth)
    }
    sel := m.selColIdx - visStart
    // Compute left padding: sum of widths (includes 1-space separators)
    left := 0
    for i := 0; i < sel; i++ {
        left += widths[i]
    }
    padLeft := strings.Repeat(" ", left)
    bar := strings.Repeat("\u2500", widths[sel]) // ─
    // Right padding to fill line
    total := 0
    for i := 0; i < len(widths); i++ { total += widths[i] }
    rightSpaces := 0
    if m.termWidth > total {
        rightSpaces = m.termWidth - (left + widths[sel])
        if rightSpaces < 0 { rightSpaces = 0 }
    } else {
        rightSpaces = total - (left + widths[sel])
        if rightSpaces < 0 { rightSpaces = 0 }
    }
    return padLeft + bar + strings.Repeat(" ", rightSpaces)
}

func (m *Model) renderFilters() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		m.styles.Base.Render("Search:"),
		m.search.View(),
		m.styles.Help.Render("Shortcuts: space=pause, t=follow, c=clear, /=search, ?=help"),
	)
}

func (m *Model) renderInspector() string {
	idx := m.tbl.Cursor()
	if idx >= 0 && idx < len(m.filtered) {
		e := m.filtered[idx]
		m.viewport.SetContent(e.PrettyJSON())
	} else {
		m.viewport.SetContent("Select a log in the table")
	}
	return m.viewport.View()
}

func (m *Model) renderHelp() string {
    return m.styles.Help.Render("Shortcuts:\n" +
        "  Space: Pause/Resume\n" +
        "  /: Search (text or /regex/), n/N: next/prev\n" +
        "  f: Filter current column\n" +
        "  f: Filters tab\n" +
        "  Enter: Inspector\n" +
        "  c: Clear visible buffer\n" +
        "  t: Toggle follow\n" +
        "  q: Quit\n" +
        "  e: Export (uses --export/--out)\n" +
        "  s: Summarize via OpenAI\n" +
        "  i: Explain via OpenAI\n" +
        "  r: Re-detect format\n" +
        "  g/G: Go to top/bottom\n" +
		"  ?: Toggle help\n" +
        "  x: Stats for current column\n" +
        "  X: Stats on selected column\n" +
        "  [: Previous columns\n" +
        "  ]: Next columns\n")
}

func (m *Model) openHelpModal() {
    m.modalActive = true
    m.modalKind = modalHelp
    m.modalTitle = "Help"
    m.modalBody = m.renderHelp()
    m.resizeModal()
}

func (m *Model) openStatsModal() {
    m.modalActive = true
    m.modalKind = modalStats
    m.modalTitle = fmt.Sprintf("Stats: %s", m.statsField)
    m.modalBody = buildStats(m.statsField, m.filtered)
    m.resizeModal()
}

func (m *Model) openInspectorModal() {
    idx := m.tbl.Cursor()
    if idx >= 0 && idx < len(m.filtered) {
        m.modalActive = true
        m.modalKind = modalInspector
        m.modalTitle = "Entry"
        m.modalBody = m.filtered[idx].PrettyJSON()
        m.resizeModal()
    }
}

func (m *Model) openSearchModal() {
    m.modalActive = true
    m.modalKind = modalSearch
    m.modalTitle = "Search"
    m.modalBody = ""
    m.resizeModal()
}

func (m *Model) resizeModal() {
    w := m.termWidth - 6
    h := m.termHeight - 6
    if w < 20 { w = 20 }
    if h < 5 { h = 5 }
    m.modalVP = viewport.New(w-4, h-4)
    if m.modalKind == modalSearch {
        // content will be dynamic; minimal body
        m.modalVP.SetContent("")
    } else {
        m.modalVP.SetContent(m.modalBody)
    }
}

func (m *Model) renderModal() string {
    // Build content
    content := ""
    switch m.modalKind {
    case modalSearch:
        content = m.search.View() + "\n[Enter]=apply  [Esc]=close  [n/N]=next/prev"
    case modalFilter:
        content = m.search.View() + "\n[Enter]=apply  [Esc]=close"
    case modalInspector, modalStats:
        content = m.modalVP.View() + "\n[Esc/Enter]=close  [C]=copy"
    default:
        content = m.modalVP.View() + "\n[Esc/Enter]=close"
    }
    boxW := m.termWidth - 6
    if boxW < 20 { boxW = 20 }
    title := m.styles.PopupTitle.Render(m.modalTitle)
    body := m.styles.PopupBox.Width(boxW).Render(title+"\n"+content)
    // Dim background block
    dim := lipgloss.NewStyle().Background(lipgloss.Color("236")).Width(m.termWidth).Height(m.termHeight).Render(" ")
    centered := lipgloss.Place(m.termWidth, m.termHeight, lipgloss.Center, lipgloss.Center, body)
    // Place centered box on dim background, then overlay over base
    return overlay(dim, centered)
}

func (m *Model) renderStats() string {
	field := m.statsField
	if field == "" {
		field = m.currentColumn()
	}
	s := buildStats(field, m.filtered)
	return m.styles.Help.Render(s)
}

func (m *Model) currentColumn() string {
	cols := m.cols
	if len(cols) == 0 {
		cols = m.schema.ColumnOrder()
	}
	if len(cols) == 0 {
		cols = []string{"ts", "level", "source", "msg", "message"}
	}
	// Prefer message; otherwise first non-preferred field
	for _, c := range cols {
		if c == "msg" || c == "message" {
			return c
		}
	}
	pref := map[string]bool{"ts": true, "time": true, "timestamp": true, "level": true, "lvl": true, "severity": true, "source": true, "component": true}
	for _, c := range cols {
		if !pref[c] {
			return c
		}
	}
	return cols[0]
}
