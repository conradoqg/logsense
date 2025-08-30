package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	"logsense/internal/util/logx"
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
	tab         tab
	state       state
	tbl         table.Model
	help        help.Model
	styles      Styles
	search      textinput.Model
	viewport    viewport.Model
	spin        spinner.Model
	keymap      KeyMap
	cols        []string
	colOffset   int
	maxCols     int
	selColIdx   int // index in full column list
	termWidth   int
	termHeight  int
	scanBufSize int

	// Dirty flags to minimize rebuilds
	rowsDirty    bool
	columnsDirty bool

	// Filter
	criteria filter.Criteria
	eval     *filter.Evaluator

	// Column sizing adjustments (by column name)
	colWidthAdj map[string]int

	// Discovered columns in order of first appearance across logs
	discovered    []string
	discoveredSet map[string]bool

	// status
	source       string
	follow       bool
	lastMsg      string
	lastSearch   string
	showHelp     bool
	showStats    bool
	statsField   string
	netBusy      bool
	failStreak   int
	prevDropped  uint64
	invalidCount int

	// Modal popup
	modalActive bool
	modalKind   modalKind
	modalVP     viewport.Model
	modalTitle  string
	modalBody   string

	// Help menu state
	helpItems []helpItem
	helpSel   int

	// Search state (navigation)
	searchActive  bool
	searchPattern string
	searchRegex   bool

	// Inline input mode (instead of modal for search/filter)
	inlineMode    inlineMode
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
	modalRaw
	modalLogs
)

type inlineMode int

const (
	inlineNone inlineMode = iota
	inlineSearch
	inlineFilter
	inlineBuffer
)

func initialModel(ctx context.Context, cfg *config.Config) *Model {
	m := &Model{
		ctx:          ctx,
		cfg:          cfg,
		ring:         model.NewRing(cfg.MaxBuffer),
		tab:          tabStream,
		state:        stateRunning,
		help:         help.New(),
		styles:       NewStyles(cfg.Theme == config.ThemeDark),
		keymap:       DefaultKeyMap(),
		search:       textinput.New(),
		spin:         spinner.New(),
		follow:       cfg.Follow,
		scanBufSize:  1024 * 1024,
		rowsDirty:    true,
		columnsDirty: true,
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
	ts.Header = lipgloss.NewStyle().PaddingRight(1)
	ts.Cell = lipgloss.NewStyle().PaddingRight(1)
	ts.Selected = m.styles.TableStyles.Selected
	m.tbl.SetStyles(ts)
	m.maxCols = 6
	m.selColIdx = 0
	m.colWidthAdj = map[string]int{}
	m.discoveredSet = map[string]bool{}
	// Initialize columns so a column is visibly selected before detection
	m.applyColumns(m.visibleColumns(m.deriveColumns()))
	return m
}

type helpItem struct {
	group string
	text  string
	key   tea.Key
}

func keyCmd(k tea.Key) tea.Cmd {
	return func() tea.Msg {
		if k.Type == tea.KeyRunes {
			return tea.KeyMsg{Type: k.Type, Runes: k.Runes}
		}
		return tea.KeyMsg{Type: k.Type}
	}
}

func keyLabel(k tea.Key) string {
	switch k.Type {
	case tea.KeyRunes:
		if len(k.Runes) == 1 {
			r := k.Runes[0]
			if r == ' ' {
				return "space"
			}
			return string(r)
		}
		return strings.ToLower(string(k.Runes))
	case tea.KeyEnter:
		return "enter"
	case tea.KeyEsc:
		return "esc"
	case tea.KeyTab:
		return "tab"
	case tea.KeyShiftTab:
		return "shift-tab"
	case tea.KeyLeft:
		return "left"
	case tea.KeyRight:
		return "right"
	case tea.KeyUp:
		return "up"
	case tea.KeyDown:
		return "down"
	case tea.KeyPgUp:
		return "pgup"
	case tea.KeyPgDown:
		return "pgdown"
	default:
		return strings.ToLower(k.String())
	}
}

func (m *Model) buildHelpItems() []helpItem {
	km := m.keymap
	items := []helpItem{
		{group: "Navigation", text: "Go to top", key: km.Top},
		{group: "Navigation", text: "Go to bottom", key: km.Bottom},
		{group: "Navigation", text: "Previous column", key: tea.Key{Type: tea.KeyLeft}},
		{group: "Navigation", text: "Next column", key: tea.Key{Type: tea.KeyRight}},
		{group: "Columns", text: "Increase column width", key: km.IncColWidth},
		{group: "Columns", text: "Decrease column width", key: km.DecColWidth},

		{group: "Search", text: "Search", key: km.Search},
		{group: "Search", text: "Search next", key: km.SearchNext},
		{group: "Search", text: "Search prev", key: km.SearchPrev},

		{group: "Filter", text: "Filter current column", key: km.Filter},
		{group: "Filter", text: "Clear filter", key: km.ClearFilter},

		{group: "Views", text: "Inspector", key: m.keymap.InspectorTab},
		{group: "Views", text: "View raw log", key: m.keymap.ViewRaw},
		{group: "Views", text: "Application logs", key: m.keymap.AppLogs},
		{group: "Views", text: "Stats for column", key: m.keymap.Stats},

		{group: "Control", text: "Pause/Resume", key: km.Pause},
		{group: "Control", text: "Toggle follow", key: km.Follow},
		{group: "Control", text: "Change buffer size", key: km.Buffer},
		{group: "Control", text: "Export", key: km.Export},
		{group: "Control", text: "Re-detect format", key: km.Redetect},
		{group: "Control", text: "Quit", key: km.Quit},
	}
	return items
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
	m.lines, m.errs = ingest.Read(m.ctx, ingest.Options{Source: src, Path: m.cfg.FilePath, Follow: m.cfg.Follow, ScanBufSize: m.scanBufSize, BlockSizeBytes: block})
	logx.Infof("ingest: source=%s path=%s follow=%v blockBytes=%d", m.source, m.cfg.FilePath, m.cfg.Follow, block)
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
		// Fit table height: reserve 1 for table header, 1 for sub-status, 1 for status
		h := msg.Height - 3
		if h < 1 {
			h = 1
		}
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
								m.searchPattern = q[1 : len(q)-1]
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
							if m.criteria.UseRegex {
								m.criteria.Query = q[1 : len(q)-1]
							}
							all := m.deriveColumns()
							if len(all) > 0 {
								if m.selColIdx >= len(all) {
									m.selColIdx = len(all) - 1
								}
								m.criteria.Field = all[m.selColIdx]
							}
							if ev, err := filter.NewEvaluator(m.criteria); err == nil {
								m.eval = ev
							}
							m.refreshFiltered()
						} else {
							m.criteria.Query = ""
							m.criteria.Field = ""
							if ev, err := filter.NewEvaluator(m.criteria); err == nil {
								m.eval = ev
							}
							m.refreshFiltered()
						}
					}
				}
				// For help modal, Enter is handled separately below
				if m.modalKind != modalHelp {
					m.modalActive = false
					return m, nil
				}
			}
			if msg.Type == tea.KeyRunes && (msg.String() == "C" || msg.String() == "c") && (m.modalKind == modalInspector || m.modalKind == modalStats) {
				copyToClipboard(m.modalBody)
				m.lastMsg = "copied to clipboard"
				return m, nil
			}
			// Help modal navigation and actions
			if m.modalKind == modalHelp {
				if msg.Type == tea.KeyUp {
					if m.helpSel > 0 {
						m.helpSel--
						m.modalVP.SetContent(m.renderHelp())
					}
					return m, nil
				}
				if msg.Type == tea.KeyDown {
					if m.helpSel+1 < len(m.helpItems) {
						m.helpSel++
						m.modalVP.SetContent(m.renderHelp())
					}
					return m, nil
				}
				if msg.Type == tea.KeyEnter {
					if len(m.helpItems) > 0 {
						it := m.helpItems[m.helpSel]
						m.modalActive = false
						return m, keyCmd(it.key)
					}
					return m, nil
				}
				if msg.Type == tea.KeyEsc || (msg.Type == tea.KeyRunes && (msg.String() == "q" || msg.String() == "?")) {
					m.modalActive = false
					return m, nil
				}
				// ignore other keys in help modal
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
		// Inline input handling for search/filter/buffer (bottom line)
		if m.inlineMode == inlineSearch || m.inlineMode == inlineFilter || m.inlineMode == inlineBuffer {
			// Enter applies; Esc cancels
			if msg.Type == tea.KeyEnter {
				q := strings.TrimSpace(m.search.Value())
				if m.inlineMode == inlineSearch {
					if m.searchEditing {
						if q != "" {
							m.searchActive = true
							if strings.HasPrefix(q, "/") && strings.HasSuffix(q, "/") && len(q) > 2 {
								m.searchRegex = true
								m.searchPattern = q[1 : len(q)-1]
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
						if m.criteria.UseRegex {
							m.criteria.Query = q[1 : len(q)-1]
						}
						all := m.deriveColumns()
						if len(all) > 0 {
							if m.selColIdx >= len(all) {
								m.selColIdx = len(all) - 1
							}
							m.criteria.Field = all[m.selColIdx]
						}
						if ev, err := filter.NewEvaluator(m.criteria); err == nil {
							m.eval = ev
						}
						m.refreshFiltered()
						m.ensureCursorVisible()
					}
					// Exit filter mode after applying
					m.inlineMode = inlineNone
					return m, nil
				} else if m.inlineMode == inlineBuffer {
					if q != "" {
						if n, err := strconv.Atoi(q); err == nil {
							if n < 50000 {
								n = 50000
							}
							m.ring.Resize(n)
							m.cfg.MaxBuffer = n
							m.lastMsg = fmt.Sprintf("max buffer set to %d lines", n)
							logx.Infof("buffer: resized to %d lines", n)
							m.refreshFiltered()
						} else {
							m.lastMsg = "invalid number"
						}
					}
					// clear input and exit buffer mode
					m.search.SetValue("")
					m.inlineMode = inlineNone
					return m, nil
				}
				return m, nil
			}
			if msg.Type == tea.KeyEsc {
				if m.inlineMode == inlineBuffer {
					m.search.SetValue("")
				}
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
			} else if m.inlineMode == inlineBuffer {
				// For buffer input, capture typing fully
				var cmd tea.Cmd
				m.search, cmd = m.search.Update(msg)
				return m, cmd
			}
			// fallthrough: let general handlers process
		}
		switch {
		case msg.Type == tea.KeyCtrlC:
			return m, tea.Quit
		case keyMatches(msg, m.keymap.IncColWidth):
			// Increase selected column width
			all := m.deriveColumns()
			if len(all) > 0 {
				if m.selColIdx >= len(all) {
					m.selColIdx = len(all) - 1
				}
				c := all[m.selColIdx]
				if m.colWidthAdj == nil {
					m.colWidthAdj = map[string]int{}
				}
				m.colWidthAdj[c] += 2
				m.refreshFiltered()
			}
			return m, nil
		case keyMatches(msg, m.keymap.DecColWidth):
			// Decrease selected column width
			all := m.deriveColumns()
			if len(all) > 0 {
				if m.selColIdx >= len(all) {
					m.selColIdx = len(all) - 1
				}
				c := all[m.selColIdx]
				if m.colWidthAdj == nil {
					m.colWidthAdj = map[string]int{}
				}
				m.colWidthAdj[c] -= 2
				m.refreshFiltered()
			}
			return m, nil
		case keyMatches(msg, m.keymap.Filter):
			// Open inline filter input on selected column
			m.search.Prompt = "f> "
			m.search.Placeholder = "filter (text or /regex/)"
			m.search.SetValue("")
			m.search.Focus()
			// Scope filter to current selected column at open
			all := m.deriveColumns()
			if len(all) > 0 {
				if m.selColIdx >= len(all) {
					m.selColIdx = len(all) - 1
				}
				m.criteria.Field = all[m.selColIdx]
			}
			m.inlineMode = inlineFilter
			return m, nil
		case keyMatches(msg, m.keymap.Buffer):
			// Open inline buffer size input, pre-filled with current value
			m.search.Prompt = "buf> "
			m.search.Placeholder = "max buffer (lines)"
			m.search.SetValue(strconv.Itoa(m.cfg.MaxBuffer))
			m.search.Focus()
			m.inlineMode = inlineBuffer
			return m, nil
		case keyMatches(msg, m.keymap.Pause):
			if m.state == stateRunning {
				m.state = statePaused
			} else {
				m.state = stateRunning
			}
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
				logx.Infof("export: wrote %d rows to %s (%s)", len(m.filtered), m.cfg.ExportOut, m.cfg.ExportFormat)
			} else {
				m.lastMsg = "use --export and --out to export"
				logx.Warnf("export: missing --export/--out flags")
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
				if m.selColIdx >= len(all) {
					m.selColIdx = len(all) - 1
				}
				m.statsField = all[m.selColIdx]
			}
			m.openStatsModal()
		case keyMatches(msg, m.keymap.ViewRaw):
			m.openRawModal()
		case keyMatches(msg, m.keymap.AppLogs):
			m.openAppLogsModal()
		case keyMatches(msg, m.keymap.NextStats):
			// Set stats to selected column (no cycling)
			all := m.deriveColumns()
			if len(all) > 0 {
				if m.selColIdx >= len(all) {
					m.selColIdx = len(all) - 1
				}
				m.statsField = all[m.selColIdx]
				m.openStatsModal()
			}
		case msg.Type == tea.KeyLeft:
			if m.selColIdx > 0 {
				m.selColIdx--
				if m.selColIdx < m.colOffset {
					if m.colOffset > 0 {
						m.colOffset--
					}
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
				if ev, err := filter.NewEvaluator(m.criteria); err == nil {
					m.eval = ev
				}
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
			logx.Infof("ingest: loading entire file")
			drain := func() tea.Msg {
				for l := range m.lines {
					e := m.parser.Parse(l.Text, l.Source)
					m.ring.Push(e)
					if m.updateDiscoveryFromEntry(e) {
						m.columnsDirty = true
					}
					m.rowsDirty = true
				}
				return loadDoneMsg{}
			}
			// Set columns from detected schema and render immediately; start async drain
			m.applyColumns(m.visibleColumns(m.deriveColumns()))
			m.rowsDirty = true
			m.columnsDirty = true
			m.refreshFiltered()
			if n := len(m.tbl.Rows()); n > 0 {
				m.tbl.SetCursor(n - 1)
			}
			return m, drain
		}
		// Set columns from detected schema and render immediately
		// Initialize selected column to msg/message or first
		all := m.deriveColumns()
		m.selColIdx = 0
		for i, c := range all {
			if c == "msg" || c == "message" {
				m.selColIdx = i
				break
			}
		}
		// Ensure selected column is visible
		if m.selColIdx >= m.colOffset+m.maxCols {
			m.colOffset = m.selColIdx - (m.maxCols - 1)
			if m.colOffset < 0 {
				m.colOffset = 0
			}
		}
		m.applyColumns(m.visibleColumns(all))
		m.rowsDirty = true
		m.columnsDirty = true
		m.refreshFiltered()
		if n := len(m.tbl.Rows()); n > 0 {
			m.tbl.SetCursor(n - 1)
		}
		// loadMoreDoneMsg removed
	case loadDoneMsg:
		m.netBusy = false
		m.lastMsg = ""
		logx.Infof("ingest: file load complete")
		m.rowsDirty = true
		m.refreshFiltered()
	case openaiStartMsg:
		m.netBusy = true
		m.lastMsg = "📡 OpenAI: inferring schema..."
		logx.Infof("openai: inferring schema for recent lines")
	case openaiDoneMsg:
		m.netBusy = false
		if msg.ok {
			m.schema = msg.schema
			p, _ := parse.NewParser(m.schema, m.cfg.TimeLayout)
			m.parser = p
			m.lastMsg = fmt.Sprintf("✅ OpenAI schema: %s (%.0f%%)", m.schema.FormatName, m.schema.Confidence*100)
			logx.Infof("openai: success format=%s strategy=%s conf=%.2f", m.schema.FormatName, m.schema.ParseStrategy, m.schema.Confidence)
		} else {
			m.lastMsg = "⚠️ OpenAI failed; keeping heuristics"
			logx.Warnf("openai: failed to infer schema; keeping heuristics")
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
						if m.updateDiscoveryFromEntry(e) {
							m.columnsDirty = true
						}
						m.rowsDirty = true
					}
				default:
					i = 999999 // break outer
				}
			}
		}
		// Drain ingest errors
		for j := 0; j < 20; j++ {
			select {
			case err, ok := <-m.errs:
				if !ok {
					j = 999999
					break
				}
				if strings.Contains(strings.ToLower(err.Error()), "token too long") {
					logx.Errorf("ingest error: %v (increase --max-buffer above %d bytes)", err, m.scanBufSize)
				} else {
					logx.Errorf("ingest error: %v", err)
				}
			default:
				j = 999999
			}
		}
		// Refresh when flagged dirty or if table has no rows but we have data
		doRefresh := m.rowsDirty || m.columnsDirty
		if !doRefresh {
			if nRows := len(m.tbl.Rows()); nRows == 0 {
				if ents, _, _ := m.ring.Snapshot(); len(ents) > 0 {
					doRefresh = true
				}
			}
		}
		if doRefresh {
			m.refreshFiltered()
			m.rowsDirty = false
			m.columnsDirty = false
		}
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
	// Remember if the cursor was at the bottom before refresh
	wasAtBottom := false
	if prev := len(m.tbl.Rows()); prev > 0 {
		if c := m.tbl.Cursor(); c >= prev-1 {
			wasAtBottom = true
		}
	}
	// Only apply field-scoped filter via m.criteria (set when applying filter)
	// Do not derive filtering from the search input; search is navigational only.
	if ev, err := filter.NewEvaluator(m.criteria); err == nil {
		m.eval = ev
	}

	rows := []table.Row{}
	entries, total, dropped := m.ring.Snapshot()
	m.total, m.dropped = total, dropped
	if m.dropped > m.prevDropped {
		delta := m.dropped - m.prevDropped
		m.prevDropped = m.dropped
		logx.Warnf("buffer overflow: dropped +%d (total=%d, cap=%d). Consider increasing --max-buffer (current=%d).", delta, m.dropped, m.ring.Cap(), m.cfg.MaxBuffer)
	}
	m.filtered = m.filtered[:0]
	// One-time discovery fallback for non-follow full file drain: if not discovered yet, derive from current entries.
	if len(m.discovered) == 0 {
		for i := range entries {
			_ = m.updateDiscoveryFromEntry(entries[i])
		}
	}
	// Determine visible columns and precompute widths once per refresh
	allCols := m.deriveColumns()
	cols := m.visibleColumns(allCols)
	widths := m.computeWidths(cols)
	// Build filtered slice from ring snapshot
	for i := range entries {
		e := entries[i]
		if m.eval != nil && !m.eval.Match(e, m.criteria) {
			continue
		}
		m.filtered = append(m.filtered, e)
	}
	m.invalidCount = 0
	for i := range m.filtered {
		e := m.filtered[i]
		row := make([]string, 0, len(cols)+1)
		if !parsedOK(e) {
			m.invalidCount++
			// First cell: invalid marker
			row = append(row, "·")
			// Spread raw text across all data columns using their visible widths
			r := []rune(e.Raw)
			pos := 0
			for j := range cols {
				cw := 0
				if j >= 0 && j < len(widths) {
					cw = widths[j]
				}
				end := pos + cw
				if end > len(r) {
					end = len(r)
				}
				seg := ""
				if cw > 0 && pos < len(r) {
					seg = string(r[pos:end])
					pos = end
				}
				row = append(row, seg)
			}
		} else {
			// First cell: blank marker for alignment
			row = append(row, " ")
			for _, c := range cols {
				cell := getCol(e, c)
				row = append(row, cell)
			}
		}
		rows = append(rows, row)
	}
	m.applyColumns(cols)
	m.tbl.SetRows(rows)
	// If we were at the bottom prior to refresh, keep sticking to the latest row
	if wasAtBottom {
		if n := len(rows); n > 0 {
			m.tbl.SetCursor(n - 1)
		}
	}
	// Keep current selection visible on refresh
	m.ensureCursorVisible()
}

func (m *Model) deriveColumns() []string {
	// Primary: discovered columns in order of first appearance across logs
	if len(m.discovered) > 0 {
		return m.discovered
	}
	// Secondary: schema-defined order if present
	cols := m.schema.ColumnOrder()
	if len(cols) > 0 {
		return cols
	}
	// Fallback to a common minimal set
	return []string{"ts", "level", "source", "msg", "message"}
}

func (m *Model) visibleColumns(all []string) []string {
	if len(all) == 0 {
		return all
	}
	if m.maxCols <= 0 {
		m.autofitMaxCols()
		if m.maxCols <= 0 {
			m.maxCols = 6
		}
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
	if len(all) == 0 {
		m.maxCols = 0
		return
	}
	width := m.termWidth
	if width <= 0 {
		width = 120
	} // default before first WindowSizeMsg
	padR := 1
	markerW := 1
	sum := 0
	count := 0
	// Track overhead while adding columns: right padding per column incl. marker and one-gutter per data col
	overhead := markerW + padR // marker cell width and its right padding
	for i := m.colOffset; i < len(all); i++ {
		c := all[i]
		// Minimal width for this column (use unselected header width and type min plus any user adjustment)
		minW := headerMinWidth(c, false)
		if m.colWidthAdj != nil {
			minW += m.colWidthAdj[c]
			if minW < headerMinWidth(c, false) {
				minW = headerMinWidth(c, false)
			}
		}
		// If we add this column, overhead grows by one gutter and one right padding
		need := sum + minW + overhead + padR + 1 // +1 gutter between columns
		if need <= width {
			sum += minW
			overhead += padR + 1
			count++
		} else {
			break
		}
	}
	if count <= 0 {
		count = 1
	}
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
	if !m.searchActive || m.searchPattern == "" {
		return
	}
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
	if !m.searchActive || m.searchPattern == "" {
		return
	}
	start := m.tbl.Cursor() - 1
	if start < 0 {
		start = len(m.filtered) - 1
	}
	for i := 0; i < len(m.filtered); i++ {
		idx := start - i
		if idx < 0 {
			idx += len(m.filtered)
		}
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
	if len(rows) == 0 {
		return
	}
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
		if err != nil {
			return false
		}
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

// updateDiscoveryFromEntry updates discovered column order using approximate
// field appearance within the raw line. Returns true if any new column was added.
func (m *Model) updateDiscoveryFromEntry(e model.LogEntry) bool {
	if m.discoveredSet == nil {
		m.discoveredSet = map[string]bool{}
	}
	changed := false
	// Build sorted keys by first index in raw text
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		if strings.TrimSpace(k) == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.SliceStable(keys, func(i, j int) bool {
		ai := strings.Index(e.Raw, keys[i])
		aj := strings.Index(e.Raw, keys[j])
		if ai == -1 && aj == -1 {
			return keys[i] < keys[j]
		}
		if ai == -1 {
			return false
		}
		if aj == -1 {
			return true
		}
		return ai < aj
	})
	for _, k := range keys {
		if !m.discoveredSet[k] {
			m.discoveredSet[k] = true
			m.discovered = append(m.discovered, k)
			changed = true
		}
	}
	return changed
}

func (m *Model) applyColumns(cols []string) {
	// Compute column widths to fit terminal (data columns only)
	widths := m.computeWidths(cols)
	// Prepend a compact marker column for valid/invalid indicator
	cs := make([]table.Column, 0, len(cols)+1)
	cs = append(cs, table.Column{Title: " ", Width: 1})
	visStart := m.colOffset
	for i, c := range cols {
		title := " " + c + " "
		abs := visStart + i
		if abs == m.selColIdx {
			// Replace padding spaces with guillemets to indicate selection
			title = "«" + c + "»"
		}
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
	// Compute available width for data columns considering marker column, cell padding, and gutters
	tableW := m.termWidth
	if tableW <= 0 {
		tableW = 120
	}
	padR := 1 // as configured in table styles
	markerW := 1
	gutters := len(cols) // spaces between marker->first and between data columns
	// total padding across all columns includes marker column too
	totalPad := (len(cols) + 1) * padR
	// data columns must fit in the remaining space
	avail := tableW - markerW - totalPad - gutters
	if avail < 10 {
		avail = 10
	}
	extra := avail - sum
	if extra != 0 {
		idx := len(cols) - 1
		for i, c := range cols {
			if c == "msg" || c == "message" {
				idx = i
				break
			}
		}
		if base[idx]+extra < 8 {
			base[idx] = 8
		} else {
			base[idx] += extra
		}
	}
	// Apply user adjustments and enforce min widths
	minW := func(c string) int {
		switch c {
		case "ts", "time", "timestamp":
			return 16
		case "level", "lvl", "severity":
			return 4
		case "msg", "message":
			return 12
		default:
			return 6
		}
	}
	for i, c := range cols {
		if m.colWidthAdj != nil {
			base[i] += m.colWidthAdj[c]
		}
		if base[i] < minW(c) {
			base[i] = minW(c)
		}
		// Ensure header fits regardless of selection state
		need := headerMinWidth(c, (m.colOffset+i) == m.selColIdx)
		if base[i] < need {
			base[i] = need
		}
	}
	// If over/under available width, adjust widths to fit exactly
	sum = 0
	for _, w := range base {
		sum += w
	}
	over := sum - avail
	if over > 0 {
		// Prefer shrinking message column
		target := -1
		for i, c := range cols {
			if c == "msg" || c == "message" {
				target = i
				break
			}
		}
		shrink := func(i int, need int) int {
			if i < 0 || i >= len(base) {
				return need
			}
			mw := headerMinWidth(cols[i], (m.colOffset+i) == m.selColIdx)
			can := base[i] - mw
			if can <= 0 {
				return need
			}
			d := need
			if d > can {
				d = can
			}
			base[i] -= d
			return need - d
		}
		over = shrink(target, over)
		for i := range base {
			if over <= 0 {
				break
			}
			if i == target {
				continue
			}
			over = shrink(i, over)
		}
	} else if over < 0 {
		// Distribute extra space, prefer message column
		need := -over
		target := -1
		for i, c := range cols {
			if c == "msg" || c == "message" {
				target = i
				break
			}
		}
		if target != -1 {
			base[target] += need
			need = 0
		}
		// If no message column, add to last column
		if need > 0 {
			base[len(base)-1] += need
		}
	}
	return base
}

// headerMinWidth returns the minimum width to fully render the header text
// including selection markers when selected.
func headerMinWidth(name string, selected bool) int {
	unsel := len([]rune(" " + name + " "))
	if !selected {
		return max(unsel, typeMin(name))
	}
	sel := len([]rune("«" + name + "»"))
	return max(max(unsel, sel), typeMin(name))
}

// typeMin provides a minimal width by common field type/name.
func typeMin(c string) int {
	switch c {
	case "ts", "time", "timestamp":
		return 16
	case "level", "lvl", "severity":
		return 4
	case "msg", "message":
		return 12
	default:
		return 6
	}
}

func (m *Model) renderStream() string {
	busy := ""
	if m.netBusy {
		busy = " " + m.spin.View()
	}
	// Table view (header already includes selection markers)
	tv := m.tbl.View()
	// Build minimal hint/status trail: only help unless in filter/buffer mode
	hint := "  [?]=help"
	if m.inlineMode == inlineFilter {
		hint += "  [enter]=apply [esc]=cancel"
	} else if m.inlineMode == inlineBuffer {
		hint += "  [enter]=apply [esc]=cancel"
	}
	// Current cursor position among filtered rows
	cur := m.tbl.Cursor()
	if cur < 0 {
		cur = -1
	}
	// Make it 1-based for display; clamp at 0 when no rows
	curDisp := 0
	total := len(m.filtered)
	if cur >= 0 && total > 0 {
		if cur >= total {
			cur = total - 1
		}
		curDisp = cur + 1
	}
	// Show rows (visible) and ingested counters to avoid confusion
	status := fmt.Sprintf("[%s] line:%d/%d rows:%d ingested:%d overflow:%d invalid:%d format:%s follow:%v source:%s%s  %s%s",
		map[state]string{stateRunning: "Running", statePaused: "Paused"}[m.state],
		curDisp, total,
		len(m.filtered), m.total, m.dropped, m.invalidCount, m.schema.FormatName, m.follow, m.source, hint, m.lastMsg, busy)
	// Inline input line above status bar (or active filter summary)
	var bottom string
	if m.inlineMode == inlineSearch {
		// Show current term and shortcuts; stays until esc (vim-like)
		term := m.search.Value()
		if m.searchEditing {
			bottom = fmt.Sprintf("search: %s    [enter]=apply [esc]=quit mode [n/N]=next/prev", term)
		} else {
			// Read-only navigation: n/N work; enter toggles back to edit
			disp := m.searchPattern
			if disp == "" {
				disp = term
			}
			bottom = fmt.Sprintf("search: %s    [enter]=edit [esc]=quit mode [n/N]=next/prev", disp)
		}
	} else if m.inlineMode == inlineFilter {
		// Show the column captured at filter-open time
		field := m.criteria.Field
		if field == "" {
			// Fallback to currently selected column if somehow unset
			all := m.deriveColumns()
			if len(all) > 0 {
				if m.selColIdx >= len(all) {
					m.selColIdx = len(all) - 1
				}
				field = all[m.selColIdx]
			} else {
				field = m.currentColumn()
			}
		}
		bottom = fmt.Sprintf("Filter %s: %s    [enter]=apply [esc]=cancel [F]=clear filter", field, m.search.View())
	} else if m.inlineMode == inlineBuffer {
		bottom = fmt.Sprintf("Max buffer (lines): %s    [enter]=apply [esc]=cancel", m.search.View())
	} else if m.criteria.Query != "" || m.criteria.Field != "" {
		// Show active filter summary when a filter is applied
		field := m.criteria.Field
		q := m.criteria.Query
		if m.criteria.UseRegex && q != "" {
			q = "/" + q + "/"
		}
		if field != "" && q != "" {
			bottom = fmt.Sprintf("Filter %s: %s    [F]=clear filter", field, q)
		} else if q != "" { // fallback
			bottom = fmt.Sprintf("Filter: %s    [F]=clear filter", q)
		}
	}
	// Always render a sub status bar to keep layout stable
	if bottom == "" {
		// minimal spacer line
		if m.termWidth > 0 {
			bottom = strings.Repeat(" ", m.termWidth)
		} else {
			bottom = ""
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, tv, bottom, m.styles.Status.Render(status))
}

func (m *Model) renderFilters() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		m.styles.Base.Render("Search:"),
		m.search.View(),
		m.styles.Help.Render("[?]=help"),
	)
}

func (m *Model) renderInspector() string {
	idx := m.tbl.Cursor()
	if idx >= 0 && idx < len(m.filtered) {
		e := m.filtered[idx]
		m.viewport.SetContent(colorizeJSONRoot(e.Fields, m.styles))
	} else {
		m.viewport.SetContent("Select a log in the table")
	}
	return m.viewport.View()
}

func (m *Model) renderHelp() string {
	// Build an organized, navigable help menu
	if len(m.helpItems) == 0 {
		m.helpItems = m.buildHelpItems()
	}
	// Ensure selection is in range
	if m.helpSel < 0 {
		m.helpSel = 0
	}
	if m.helpSel >= len(m.helpItems) {
		m.helpSel = len(m.helpItems) - 1
	}
	lines := []string{"Shortcuts:"}
	currentGroup := ""
	lineIndexOfSel := 0
	for i, it := range m.helpItems {
		if it.group != currentGroup {
			currentGroup = it.group
			lines = append(lines, "")
			lines = append(lines, currentGroup+":")
		}
		prefix := "  "
		if i == m.helpSel {
			prefix = "> "
			lineIndexOfSel = len(lines)
		}
		key := keyLabel(it.key)
		lines = append(lines, fmt.Sprintf("%s[%s] %s", prefix, key, it.text))
	}
	// Adjust viewport to keep selection visible
	if m.modalVP.Height > 0 {
		top := m.modalVP.YOffset
		bottom := top + m.modalVP.Height - 1
		if lineIndexOfSel <= top {
			if lineIndexOfSel-1 >= 0 {
				m.modalVP.YOffset = lineIndexOfSel - 1
			} else {
				m.modalVP.YOffset = 0
			}
		} else if lineIndexOfSel >= bottom {
			m.modalVP.YOffset = lineIndexOfSel - m.modalVP.Height + 2
			if m.modalVP.YOffset < 0 {
				m.modalVP.YOffset = 0
			}
		}
	}
	return m.styles.Help.Render(strings.Join(lines, "\n"))
}

func (m *Model) openHelpModal() {
	m.modalActive = true
	m.modalKind = modalHelp
	m.modalTitle = "Help"
	m.helpItems = m.buildHelpItems()
	m.helpSel = 0
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
		m.modalBody = colorizeJSONRoot(m.filtered[idx].Fields, m.styles)
		m.resizeModal()
	}
}

func (m *Model) openRawModal() {
	idx := m.tbl.Cursor()
	if idx >= 0 && idx < len(m.filtered) {
		m.modalActive = true
		m.modalKind = modalRaw
		m.modalTitle = "Raw Log"
		m.modalBody = m.filtered[idx].Raw
		m.resizeModal()
	}
}

func (m *Model) openAppLogsModal() {
	m.modalActive = true
	m.modalKind = modalLogs
	m.modalTitle = "Application Logs"
	m.modalBody = logx.Dump()
	m.resizeModal()
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
	if w < 20 {
		w = 20
	}
	if h < 5 {
		h = 5
	}
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
	case modalHelp:
		// Update content dynamically for help menu
		m.modalVP.SetContent(m.renderHelp())
		content = m.modalVP.View() + "\n[esc]=close  [enter]=run"
	case modalSearch:
		content = m.search.View() + "\n[enter]=apply  [esc]=close  [n/N]=next/prev"
	case modalFilter:
		content = m.search.View() + "\n[enter]=apply  [esc]=close"
	case modalInspector, modalStats, modalRaw, modalLogs:
		content = m.modalVP.View() + "\n[esc/enter]=close  [C]=copy"
	default:
		content = m.modalVP.View() + "\n[esc/enter]=close"
	}
	boxW := m.termWidth - 6
	if boxW < 20 {
		boxW = 20
	}
	title := m.styles.PopupTitle.Render(m.modalTitle)
	body := m.styles.PopupBox.Width(boxW).Render(title + "\n" + content)
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
