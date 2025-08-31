package ui

import (
	"regexp"
	"strings"

	"logsense/internal/model"
)

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
