package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

func (m *Model) View() string {
    switch m.tab {
    case tabStream:
        v := m.renderStream()
        if m.modalActive {
            // Dim the background content while keeping it visible
            dimmed := lipgloss.NewStyle().Faint(true).Render(v)
            v = overlay(dimmed, m.renderModal())
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

func (m *Model) renderStream() string {
	// Table view (header already includes selection markers)
	tv := m.tbl.View()
	// Build minimal hint/status trail
	hint := "[?]=help"
	if m.inlineMode == inlineFilter {
		hint += "[enter]=apply [esc]=cancel"
	} else if m.inlineMode == inlineBuffer {
		hint += "[enter]=apply [esc]=cancel"
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
    // Compact status: only running state, follow state, current line/total
    rate := m.rateEWMA
    // Display rate with 1 decimal if meaningful
    rateStr := "0/s"
    if rate >= 0.05 { // avoid noise
        rateStr = fmt.Sprintf("%.1f/s", rate)
    }
    status := fmt.Sprintf("[%s] | line:%d/%d rate:%s follow:%v | %s | %s",
        map[state]string{stateRunning: "Running", statePaused: "Paused"}[m.state],
        curDisp, total,
        rateStr,
        m.follow, hint, m.lastMsg)
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
    // Build items and render with fixed split for label/bars
    m.buildAndRenderStats()
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
	m.modalBody = logxDump()
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
    } else if m.modalKind == modalStats {
        // Re-render stats to fit new size
        m.buildAndRenderStats()
        m.modalVP.SetContent(m.modalBody)
    } else if m.modalKind == modalStatsTime {
        // Re-render time distribution with new size
        m.renderStatsTime()
        m.modalVP.SetContent(m.modalBody)
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
		// Place localized controls on a dedicated hint line inside modal
		content = m.modalVP.View() + "\n[esc]=close  [enter]=run"
	case modalSearch:
		content = m.search.View() + "\n[enter]=apply  [esc]=close  [n/N]=next/prev"
	case modalFilter:
		content = m.search.View() + "\n[enter]=apply  [esc]=close"
    case modalInspector, modalRaw, modalExplain:
        content = m.modalVP.View() + "\n[esc/enter]=close  [c]=copy"
    case modalStats:
        content = m.modalVP.View() + "\n[esc]=close  [enter]=open  [↑/↓]=navigate  [c]=copy"
    case modalStatsTime:
        content = m.modalVP.View() + "\n[esc]=back  [enter]=close  [c]=copy"
    case modalLogs:
        // Fixed status header above navigable application log viewport
        header := []string{
            "Status:",
            fmt.Sprintf("format: %s (%s)", m.schema.FormatName, m.schema.ParseStrategy),
            fmt.Sprintf("rows: %d  ingested: %d  overflow: %d  invalid: %d", len(m.filtered), m.total, m.dropped, m.invalidCount),
            fmt.Sprintf("source: %s  follow: %v", m.source, m.follow),
        }
        h := m.styles.Help.Render(strings.Join(header, "\n"))
        content = h + "\n" + m.modalVP.View() + "\n[esc/enter]=close  [c]=copy"
	default:
		content = m.modalVP.View() + "\n[esc/enter]=close"
	}
    boxW := m.termWidth - 6
    if boxW < 20 {
        boxW = 20
    }
    title := m.styles.PopupTitle.Render(m.modalTitle)
    body := m.styles.PopupBox.Width(boxW).Render(title + "\n" + content)
    // Center the modal box; do not cover entire background to keep it dimmed not dark
    centered := lipgloss.Place(m.termWidth, m.termHeight, lipgloss.Center, lipgloss.Center, body)
    return centered
}

func (m *Model) renderStats() string {
    field := m.statsField
    if field == "" {
        field = m.currentColumn()
    }
    // Keep legacy function available; no longer used for modal rendering
    s := buildStats(field, m.filtered)
    return m.styles.Help.Render(s)
}

// buildAndRenderStats computes stats items for the current field and renders
// them into the modal body using a fixed 50/50 split for label and bars.
func (m *Model) buildAndRenderStats() {
    field := m.statsField
    if field == "" {
        field = m.currentColumn()
        m.statsField = field
    }
    // Preserve current selection identity across recomputes
    var prev statItem
    hasPrev := false
    if m.statsSel >= 0 && m.statsSel < len(m.statsItems) {
        prev = m.statsItems[m.statsSel]
        hasPrev = true
    }
    items := computeStatsItems(field, m.filtered)
    m.statsItems = items
    if len(items) == 0 {
        m.statsSel = 0
    } else if hasPrev {
        // Try to locate the previous selected item in the new list
        idx := -1
        for i, it := range items {
            if prev.hasRange && it.hasRange {
                if it.low == prev.low && it.high == prev.high { idx = i; break }
            } else if prev.hasExact && it.hasExact {
                if it.fvalue == prev.fvalue { idx = i; break }
            } else if prev.svalue != "" && it.svalue != "" {
                if it.svalue == prev.svalue { idx = i; break }
            }
        }
        if idx >= 0 { m.statsSel = idx } else if m.statsSel >= len(items) { m.statsSel = len(items) - 1 }
        if m.statsSel < 0 { m.statsSel = 0 }
    } else if m.statsSel >= len(items) {
        m.statsSel = 0
    }
    // Render with current modal width
    width := m.modalVP.Width
    if width <= 0 {
        width = max(40, m.termWidth-10)
    }
    m.modalBody = renderStatsList(items, width, m.statsSel)
    m.modalVP.SetContent(m.modalBody)
    // Keep selected line visible in the stats viewport
    if m.modalVP.Height > 0 {
        top := m.modalVP.YOffset
        bottom := top + m.modalVP.Height - 1
        line := m.statsSel // one item per line, no header
        if line <= top {
            if line-1 >= 0 {
                m.modalVP.YOffset = line - 1
            } else {
                m.modalVP.YOffset = 0
            }
        } else if line >= bottom {
            m.modalVP.YOffset = line - m.modalVP.Height + 2
            if m.modalVP.YOffset < 0 {
                m.modalVP.YOffset = 0
            }
        }
    }
}

// renderStatsTime rebuilds the time-distribution chart for the selected stats item.
func (m *Model) renderStatsTime() {
    width := m.modalVP.Width
    height := m.modalVP.Height
    if width < 20 {
        width = 20
    }
    if height < 6 {
        height = 6
    }
    content := buildTimeDistribution(m.statsField, m.statsItems, m.statsSel, m.filtered, width, height)
    m.modalBody = content
    m.modalVP.SetContent(content)
}

// openStatsTrendModal opens the time-distribution chart for current selection.
func (m *Model) openStatsTrendModal() {
    if m.statsSel < 0 || m.statsSel >= len(m.statsItems) {
        return
    }
    it := m.statsItems[m.statsSel]
    m.modalActive = true
    m.modalKind = modalStatsTime
    m.modalTitle = fmt.Sprintf("%s over time: %s", m.statsField, it.label)
    m.renderStatsTime()
    m.resizeModal()
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
