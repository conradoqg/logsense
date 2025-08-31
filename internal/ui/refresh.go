package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"

	"logsense/internal/filter"
	"logsense/internal/model"
	"logsense/internal/parse"
	"logsense/internal/util/logx"
)

func (m *Model) refreshFiltered() {
	// Remember if the cursor was at the bottom before refresh
	wasAtBottom := false
	if prev := len(m.tbl.Rows()); prev > 0 {
		if c := m.tbl.Cursor(); c >= prev-1 {
			wasAtBottom = true
		}
	}
	// Only apply field-scoped filter via m.criteria (set when applying filter)
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
}

// deriveColumns returns the preferred ordering of columns.
func (m *Model) deriveColumns() []string {
	// Prefer schema-defined order when available (e.g., after LLM or heuristics)
	if len(m.schema.Fields) > 0 {
		cols := m.schema.ColumnOrder()
		if len(cols) > 0 {
			return cols
		}
	}
	// Otherwise, use discovered order in the stream
	if len(m.discovered) > 0 {
		return m.discovered
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

// Preferred default widths per column type
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

// applyNewSchema sets the active schema and parser, then re-parses
// the current ring buffer so columns and rows reflect the new schema.
func (m *Model) applyNewSchema(s model.Schema, reason string) {
	logx.Infof("schema: applying new schema via %s: format=%s strategy=%s", reason, s.FormatName, s.ParseStrategy)
	m.schema = s
	p, _ := parse.NewParser(m.schema, m.cfg.TimeLayout)
	m.parser = p
	// Re-parse existing buffer
	old, _, _ := m.ring.Snapshot()
	nr := model.NewRing(m.cfg.MaxBuffer)
	// Reset discovery so it can rebuild in new order
	m.discovered = nil
	m.discoveredSet = map[string]bool{}
	// Rebuild field set and sample row based on re-parsed entries,
	// mirroring initial detection behavior so schema columns reflect actual data.
	fieldSet := map[string]struct{}{}
	var sampleRow map[string]any
	for i := range old {
		e := p.Parse(old[i].Raw, old[i].Source)
		if sampleRow == nil {
			sampleRow = e.Fields
		}
		for k := range e.Fields {
			if strings.TrimSpace(k) == "" {
				continue
			}
			fieldSet[k] = struct{}{}
		}
		nr.Push(e)
		_ = m.updateDiscoveryFromEntry(e)
	}
	// Only override schema fields if we discovered at least one non-generic field
	// (avoids wiping LLM-provided field list when regex didn't match and only 'msg' was set).
	nonGeneric := 0
	for k := range fieldSet {
		if k != "msg" && k != "message" {
			nonGeneric++
		}
	}
	if nonGeneric > 0 {
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
	m.ring = nr
	// Reset selection to prioritize showing a domain-specific field if present
	all := m.deriveColumns()
	// Recompute how many columns fit for the new schema
	m.autofitMaxCols()
	pref := map[string]bool{"ts": true, "time": true, "timestamp": true, "level": true, "lvl": true, "severity": true, "source": true, "component": true, "msg": true, "message": true}
	// Default to message when available; otherwise first column
	m.selColIdx = 0
	for i, c := range all {
		if c == "msg" || c == "message" {
			m.selColIdx = i
			break
		}
	}
	// If there is any non-preferred column, prefer selecting the first one to make the schema change visible
	for i, c := range all {
		if !pref[c] {
			m.selColIdx = i
			break
		}
	}
	if m.selColIdx >= m.colOffset+m.maxCols {
		m.colOffset = m.selColIdx - (m.maxCols - 1)
		if m.colOffset < 0 {
			m.colOffset = 0
		}
	}
	m.columnsDirty = true
	m.rowsDirty = true
	// Hard reset table model to ensure column changes reflect immediately
	h := m.tbl.Height()
	m.tbl = table.New(table.WithFocused(true), table.WithHeight(h))
	ts := table.DefaultStyles()
	ts.Header = lipgloss.NewStyle().PaddingRight(1)
	ts.Cell = lipgloss.NewStyle().PaddingRight(1)
	ts.Selected = m.styles.TableStyles.Selected
	m.tbl.SetStyles(ts)
	// Apply columns ASAP so users see the updated schema
	logx.Infof("schema: derived columns after %s = %v", reason, all)
	m.applyColumns(m.visibleColumns(all))
	m.refreshFiltered()
}
