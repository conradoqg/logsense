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
	prev := len(m.tbl.Rows())
	if prev > 0 {
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
	// Build a local filtered slice to avoid indexing into m.filtered while
	// another concurrent refresh may reslice it. We'll assign back at the end.
	localFiltered := make([]model.LogEntry, 0, len(entries))
	// One-time discovery fallback for non-follow full file drain: if not discovered yet, derive from current entries.
	if len(m.discovered) == 0 {
		for i := range entries {
			_ = m.updateDiscoveryFromEntry(entries[i])
		}
	}
	// Recompute how many columns fit given current terminal and adjustments
	m.autofitMaxCols()
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
		localFiltered = append(localFiltered, e)
	}
	m.invalidCount = 0
	// Iterate over the local snapshot to avoid out-of-range panics if
	// m.filtered is concurrently modified by another refresh.
	for i := range localFiltered {
		e := localFiltered[i]
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
	// Publish the new filtered slice atomically before applying UI updates.
	m.filtered = localFiltered
	m.applyColumns(cols)
	m.tbl.SetRows(rows)
	// If we were at the bottom prior to refresh, keep sticking to the latest row
	if wasAtBottom {
		if n := len(rows); n > 0 {
			m.tbl.SetCursor(n - 1)
			m.ensureCursorVisible()
		}
	} else if prev == 0 {
		// First population: select last row by default for better visibility
		if n := len(rows); n > 0 {
			m.tbl.SetCursor(n - 1)
			m.ensureCursorVisible()
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
	// Effective table width
	width := m.termWidth
	if width <= 0 {
		width = 120
	} // default before first WindowSizeMsg
	padR := 1 // table cell right padding
	markerW := 1

	// Helper to compute the minimal width needed for a slice of columns
	minNeed := func(start, count int) int {
		if count <= 0 {
			return 0
		}
		// Data columns minimal widths + gutters + overall padding + marker
		sum := 0
		for j := 0; j < count; j++ {
			idx := start + j
			if idx < 0 || idx >= len(all) {
				break
			}
			name := all[idx]
			// Base minimal width: use unselected header to keep a stable
			// column count when navigating (selection markers are visual only).
			sel := false
			mw := headerMinWidth(name, sel)
			// Apply user adjustments (clamped by type/header minimum)
			if m.colWidthAdj != nil {
				mw += m.colWidthAdj[name]
				baseMin := headerMinWidth(name, sel)
				if mw < baseMin {
					mw = baseMin
				}
			}
			// Include inter-column gutter except for last; we'll add gutters separately below
			sum += mw
			if j < count-1 {
				sum += 1 // gutter between data columns
			}
		}
		// Total padding: marker + (data cols + marker) right padding
		totalPad := (count + 1) * padR
		// Add marker cell width
		need := sum + markerW + totalPad
		// Also account for gutter between marker and first data column
		if count > 0 {
			need += 1
		}
		return need
	}

	// Greedily add columns while the minimal required width fits.
	max := 0
	for n := 1; m.colOffset+n <= len(all); n++ {
		need := minNeed(m.colOffset, n)
		if need <= width {
			max = n
			continue
		}
		break
	}
	if max <= 0 {
		max = 1
	}
	m.maxCols = max
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
	// Avoid Bubbles table rendering rows with mismatched column count by
	// clearing rows before changing columns. Callers set rows right after.
	m.tbl.SetRows([]table.Row{})
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
		// Ensure header baseline fits (ignore selection markers to avoid
		// column-count changes or overflow when selecting the last column).
		need := headerMinWidth(c, false)
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
		// Prefer shrinking message column, but avoid shrinking the
		// currently selected column to preserve user adjustments.
		selectedVis := -1
		for i := range cols {
			if (m.colOffset + i) == m.selColIdx {
				selectedVis = i
				break
			}
		}
		target := -1
		for i, c := range cols {
			if (c == "msg" || c == "message") && i != selectedVis {
				target = i
				break
			}
		}
		if target == -1 {
			// Fallback: last non-selected column
			for i := len(cols) - 1; i >= 0; i-- {
				if i != selectedVis {
					target = i
					break
				}
			}
		}
		shrink := func(i int, need int) int {
			if i < 0 || i >= len(base) {
				return need
			}
			// Allow shrinking down to baseline header width (ignoring
			// selection markers) to prevent overflow/wrapping.
			mw := headerMinWidth(cols[i], false)
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
			if i == target || i == selectedVis {
				continue
			}
			over = shrink(i, over)
		}
		// As a last resort, allow shrinking the selected column down to
		// baseline header width to eliminate any remaining overflow.
		if over > 0 && selectedVis != -1 {
			over = shrink(selectedVis, over)
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
