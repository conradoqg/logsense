package ui

import (
    "fmt"
    "strconv"
    "strings"
    "time"

    tea "github.com/charmbracelet/bubbletea"

    "logsense/internal/export"
    "logsense/internal/filter"
    "logsense/internal/util/logx"
)

func (m *Model) buildHelpItems() []helpItem {
    km := m.keymap
    items := []helpItem{
        // Navigation between rows and columns
        {group: "Navigation", text: "Previous row", key: tea.Key{Type: tea.KeyUp}},
        {group: "Navigation", text: "Next row", key: tea.Key{Type: tea.KeyDown}},
        {group: "Navigation", text: "Page up", key: tea.Key{Type: tea.KeyPgUp}},
        {group: "Navigation", text: "Page down", key: tea.Key{Type: tea.KeyPgDown}},
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
        {group: "Views", text: "Stats (selected column)", key: m.keymap.NextStats},
        {group: "Views", text: "Stream tab", key: m.keymap.StreamTab},
        {group: "Views", text: "Filters tab", key: m.keymap.FilterTab},

        {group: "Control", text: "Pause/Resume", key: km.Pause},
        {group: "Control", text: "Toggle follow", key: km.Follow},
        {group: "Control", text: "Change buffer size", key: km.Buffer},
        {group: "Control", text: "Export", key: km.Export},
        {group: "Control", text: "Re-detect format", key: km.Redetect},
        {group: "Control", text: "Help", key: km.Help},
        {group: "Control", text: "Quit", key: km.Quit},

        {group: "Actions", text: "Copy current line", key: km.CopyLine},

        {group: "AI", text: "Summarize log (OpenAI)", key: km.Summarize},
        {group: "AI", text: "Explain log (OpenAI)", key: km.Explain},
    }
    return items
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
            }
        }

        // Shortcuts
        switch {
        case keyMatches(msg, m.keymap.IncColWidth):
            all := m.deriveColumns()
            if len(all) > 0 {
                c := all[m.selColIdx]
                m.colWidthAdj[c] += 2
                m.columnsDirty = true
                m.applyColumns(m.visibleColumns(all))
                m.refreshFiltered()
            }
            return m, nil
        case keyMatches(msg, m.keymap.DecColWidth):
            all := m.deriveColumns()
            if len(all) > 0 {
                c := all[m.selColIdx]
                m.colWidthAdj[c] -= 2
                m.columnsDirty = true
                m.applyColumns(m.visibleColumns(all))
                m.refreshFiltered()
            }
            return m, nil
        case keyMatches(msg, m.keymap.Filter):
            // Start inline filter for selected column
            m.inlineMode = inlineFilter
            all := m.deriveColumns()
            if len(all) > 0 {
                if m.selColIdx >= len(all) {
                    m.selColIdx = len(all) - 1
                }
                m.criteria.Field = all[m.selColIdx]
            }
            return m, nil
        case keyMatches(msg, m.keymap.Buffer):
            m.inlineMode = inlineBuffer
            m.search.SetValue("")
            return m, nil
        case keyMatches(msg, m.keymap.Pause):
            if m.state == stateRunning {
                m.state = statePaused
            } else {
                m.state = stateRunning
            }
            return m, nil
        case keyMatches(msg, m.keymap.Follow):
            m.follow = !m.follow
            // Restart pipeline with new follow state; preserve tail offset if possible
            if m.follow {
                m.tailStartOffset = -1 // let ingest pick current end
            }
            return m, setupPipeline(m)
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
            return m, nil
        case keyMatches(msg, m.keymap.Help):
            m.openHelpModal()
            return m, nil
        case keyMatches(msg, m.keymap.StreamTab):
            m.tab = tabStream
            return m, nil
        case keyMatches(msg, m.keymap.FilterTab):
            m.tab = tabFilters
            return m, nil
        case keyMatches(msg, m.keymap.InspectorTab):
            m.openInspectorModal()
            return m, nil
        case keyMatches(msg, m.keymap.Stats):
            all := m.deriveColumns()
            if len(all) > 0 {
                if m.selColIdx >= len(all) {
                    m.selColIdx = len(all) - 1
                }
                m.statsField = all[m.selColIdx]
            }
            m.openStatsModal()
            return m, nil
        case keyMatches(msg, m.keymap.ViewRaw):
            m.openRawModal()
            return m, nil
        case keyMatches(msg, m.keymap.AppLogs):
            m.openAppLogsModal()
            return m, nil
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
            return m, nil
        case msg.Type == tea.KeyLeft:
            all := m.deriveColumns()
            if m.selColIdx > 0 {
                m.selColIdx--
                if m.selColIdx < m.colOffset {
                    m.colOffset = m.selColIdx
                }
                m.applyColumns(m.visibleColumns(all))
                m.refreshFiltered()
            }
            return m, nil
        case msg.Type == tea.KeyRight:
            all := m.deriveColumns()
            if m.selColIdx+1 < len(all) {
                m.selColIdx++
                if m.selColIdx >= m.colOffset+m.maxCols {
                    m.colOffset = m.selColIdx - (m.maxCols - 1)
                    if m.colOffset < 0 {
                        m.colOffset = 0
                    }
                }
                m.applyColumns(m.visibleColumns(all))
                m.refreshFiltered()
            }
            return m, nil
        case keyMatches(msg, m.keymap.Top):
            m.tbl.SetCursor(0)
            return m, nil
        case keyMatches(msg, m.keymap.Bottom):
            if n := len(m.tbl.Rows()); n > 0 {
                m.tbl.SetCursor(n - 1)
            }
            return m, nil
        case keyMatches(msg, m.keymap.Summarize):
            m.lastMsg = "Summarize (OpenAI) unavailable in offline mode"
            return m, nil
        case keyMatches(msg, m.keymap.Explain):
            m.lastMsg = "Explain (OpenAI) unavailable in offline mode"
            return m, nil
        case keyMatches(msg, m.keymap.Redetect):
            return m, m.triggerRedetect()
        case keyMatches(msg, m.keymap.Search):
            // Toggle inline search mode
            if m.inlineMode == inlineSearch {
                // if already searching, toggle edit state
                m.searchEditing = !m.searchEditing
                if m.searchEditing {
                    m.search.Focus()
                }
            } else {
                m.inlineMode = inlineSearch
                m.searchEditing = true
                m.search.Focus()
            }
            return m, nil
        case keyMatches(msg, m.keymap.SearchNext):
            if m.searchActive {
                m.searchNext()
                return m, nil
            }
        case keyMatches(msg, m.keymap.SearchPrev):
            if m.searchActive {
                m.searchPrev()
                return m, nil
            }
        case keyMatches(msg, m.keymap.ClearFilter):
            m.criteria.Query = ""
            m.criteria.Field = ""
            if ev, err := filter.NewEvaluator(m.criteria); err == nil {
                m.eval = ev
            }
            m.refreshFiltered()
            return m, nil
        case keyMatches(msg, m.keymap.CopyLine):
            idx := m.tbl.Cursor()
            if idx >= 0 && idx < len(m.filtered) {
                copyToClipboard(m.filtered[idx].Raw)
                m.lastMsg = "copied to clipboard"
            }
            return m, nil
        case keyMatches(msg, m.keymap.Quit):
            return m, tea.Quit
        }
    case detectedMsg:
        drain := func() tea.Msg {
            // Drain remaining lines when not following; record file size
            if !m.follow {
                // Wait briefly for ingest to finish
                time.Sleep(200 * time.Millisecond)
                m.netBusy = false
                m.lastMsg = ""
            }
            m.applyColumns(m.visibleColumns(m.deriveColumns()))
            m.rowsDirty = true
            m.columnsDirty = true
            m.refreshFiltered()
            if n := len(m.tbl.Rows()); n > 0 {
                m.tbl.SetCursor(n - 1)
            }
            return loadDoneMsg{}
        }
        // Set columns from detected schema and render immediately
        // Initialize selected column to msg/message or first
        m.lastMsg = fmt.Sprintf("Detected (heuristics): %s", m.schema.FormatName)
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
        return m, drain
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
    case redetectMsg:
        // Apply new schema from a manual re-detect
        m.applyNewSchema(msg.schema, "Re-detect")
        m.lastMsg = fmt.Sprintf("🔄 Schema updated: %s (%s)", m.schema.FormatName, m.schema.ParseStrategy)
        return m, nil
    case toastMsg:
        m.lastMsg = msg.text
        return m, nil
    case openaiDoneMsg:
        m.netBusy = false
        if msg.ok {
            // Apply new schema and re-parse existing buffer
            m.applyNewSchema(msg.schema, "OpenAI")
            m.lastMsg = fmt.Sprintf("✅ OpenAI schema: %s (%.0f%%)", m.schema.FormatName, m.schema.Confidence*100)
            logx.Infof("openai: success format=%s strategy=%s conf=%.2f", m.schema.FormatName, m.schema.ParseStrategy, m.schema.Confidence)
            // Save to cache if enabled and we have a file path
            if !m.cfg.NoCache && strings.TrimSpace(m.cfg.FilePath) != "" {
                if err := detectSaveSchema(m.cfg.FilePath, m.schema); err != nil {
                    logx.Warnf("detect: failed to save schema cache: %v", err)
                } else {
                    logx.Infof("detect: schema cached for %s", m.cfg.FilePath)
                }
            } else if m.cfg.NoCache {
                logx.Infof("detect: not caching schema due to --no-cache")
            }
        } else {
            if strings.TrimSpace(msg.err) != "" {
                m.lastMsg = fmt.Sprintf("⚠️ OpenAI failed: %s", msg.err)
                logx.Warnf("openai: failed to infer schema: %s", msg.err)
            } else {
                m.lastMsg = "⚠️ OpenAI failed; keeping heuristics"
                logx.Warnf("openai: failed to infer schema; keeping heuristics")
            }
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
    return m, cmd
}
