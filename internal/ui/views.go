package ui

import (
    "encoding/base64"
    "fmt"
    "math"
    "os"
    "regexp"
    "sort"
    "strconv"
    "strings"
    "time"

	"logsense/internal/model"
)

func overlay(base, overlay string) string {
	// Draw overlay on top of base by replacing lines where overlay has content.
	bLines := strings.Split(base, "\n")
	oLines := strings.Split(overlay, "\n")
	// Pad to same length
	maxLen := len(bLines)
	if len(oLines) > maxLen {
		maxLen = len(oLines)
	}
	for len(bLines) < maxLen {
		bLines = append(bLines, "")
	}
	for len(oLines) < maxLen {
		oLines = append(oLines, "")
	}
	out := make([]string, maxLen)
    for i := 0; i < maxLen; i++ {
        // Treat whitespace-only overlay lines as transparent
        if strings.TrimSpace(oLines[i]) != "" {
            out[i] = oLines[i]
        } else {
            out[i] = bLines[i]
        }
    }
	return strings.Join(out, "\n")
}

// copyToClipboard tries to copy text using OSC52 (works in many terminals).
func copyToClipboard(s string) {
	// Remove ANSI color codes before copying
	s = stripANSI(s)
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	payload := fmt.Sprintf("\x1b]52;c;%s\x07", enc)
	// Best-effort: write to /dev/tty to avoid clobbering the app's stdout buffer
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		defer f.Close()
		_, _ = f.WriteString(payload)
		return
	}
	// Fallback to stdout
	fmt.Fprint(os.Stdout, payload)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

func parsedOK(e model.LogEntry) bool {
	if e.Timestamp != nil || e.Level != "" {
		return true
	}
	if len(e.Fields) >= 2 {
		return true
	}
	if _, ok := e.Fields["msg"]; ok {
		return true
	}
	if _, ok := e.Fields["message"]; ok {
		return true
	}
	return false
}

func buildStats(field string, entries []model.LogEntry) string {
	// collect values
	nums := []float64{}
	counts := map[string]int{}
	for _, e := range entries {
		v, ok := e.Fields[field]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case float64:
			nums = append(nums, t)
		case int, int32, int64:
			nums = append(nums, float64(asInt64(v)))
		case string:
			if f, ok := tryParseFloat(t); ok {
				nums = append(nums, f)
			} else {
				counts[t]++
			}
		default:
			// ignore
		}
	}
	if len(nums) > 0 {
		// If few distinct numeric values, show categorical-like view
		uniq := map[float64]int{}
		for _, v := range nums {
			uniq[v]++
		}
		if len(uniq) <= 40 {
			// Build labeled counts for numeric values
			cat := map[string]int{}
			for val, c := range uniq {
				label := formatNumericLabel(val)
				cat[label] = c
			}
			return categoricalStats(field, cat)
		}
		return numericStats(field, nums)
	}
	return categoricalStats(field, counts)
}

func formatNumericLabel(v float64) string {
	if v == float64(int64(v)) {
		return sprintf("%.0f", v)
	}
	return sprintf("%.2f", v)
}

func numericStats(field string, vals []float64) string {
	if len(vals) == 0 {
		return "No data"
	}
	min, max, sum := vals[0], vals[0], 0.0
	distinct := map[float64]struct{}{}
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
		sum += v
		distinct[v] = struct{}{}
	}
	mean := sum / float64(len(vals))
	// histogram bins adapt to distinct values (5..40)
	uniq := map[float64]struct{}{}
	for _, v := range vals {
		uniq[v] = struct{}{}
	}
	uniqCount := len(uniq)
	bins := uniqCount
	if bins < 5 {
		bins = 5
	}
	if bins > 40 {
		bins = 40
	}
	hist := make([]int, bins)
	edges := make([]float64, bins+1)
	step := 0.0
	if bins > 0 {
		step = (max - min) / float64(bins)
	}
	if max == min || step == 0 {
		hist[0] = len(vals)
		edges[0], edges[1] = min, max
	} else {
		for _, v := range vals {
			idx := int(math.Floor(float64(bins) * (v - min) / (max - min)))
			if idx >= bins {
				idx = bins - 1
			}
			if idx < 0 {
				idx = 0
			}
			hist[idx]++
		}
		for i := 0; i <= bins; i++ {
			edges[i] = min + float64(i)*step
		}
	}
	maxc := 1
	for _, c := range hist {
		if c > maxc {
			maxc = c
		}
	}
	var b strings.Builder
	b.WriteString("Stats for ")
	b.WriteString(field)
	b.WriteString(" (numeric):\n")
	b.WriteString(sprintf("min=%.2f mean=%.2f max=%.2f n=%d distinct=%d\n", min, mean, max, len(vals), len(distinct)))
	for i := 0; i < bins; i++ {
		width := int(math.Round(20 * float64(hist[i]) / float64(maxc)))
		bar := colorBar(width, float64(hist[i]), float64(maxc))
		// Label bins by range [edge_i, edge_{i+1}]
		low := edges[i]
		high := edges[minInt(i+1, len(edges)-1)]
		label := sprintf("[%.2f – %.2f]", low, high)
		b.WriteString(sprintf("%-18s %s (%d)\n", label, bar, hist[i]))
	}
	return b.String()
}

func categoricalStats(field string, counts map[string]int) string {
	if len(counts) == 0 {
		return "No data"
	}
	// show all (viewport is scrollable)
	type kv struct {
		k string
		v int
	}
	arr := make([]kv, 0, len(counts))
	for k, v := range counts {
		arr = append(arr, kv{k, v})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].v > arr[j].v })
	// no truncation; viewport handles scrolling
	maxc := arr[0].v
	var b strings.Builder
	b.WriteString("Stats for ")
	b.WriteString(field)
	b.WriteString(" (categorical):\n")
	for _, it := range arr {
		width := int(math.Round(20 * float64(it.v) / float64(maxc)))
		bar := colorBar(width, float64(it.v), float64(maxc))
		b.WriteString(sprintf("%-12s | %s (%d)\n", it.k, bar, it.v))
	}
	return b.String()
}

// colorBar returns a bar with simple red intensity for larger ratios.
func colorBar(width int, val, max float64) string {
	if width <= 0 {
		return ""
	}
	r := 0.0
	if max > 0 {
		r = val / max
	}
	color := 226 - int(r*30) // yellow->red
	if color < 196 {
		color = 196
	}
	bar := strings.Repeat("▇", width)
	return sprintf("\x1b[38;5;%dm%s\x1b[0m", color, bar)
}

// computeStatsItems builds the stats list as structured items for navigation.
func computeStatsItems(field string, entries []model.LogEntry) []statItem {
    nums := []float64{}
    counts := map[string]int{}
    for _, e := range entries {
        v, ok := e.Fields[field]
        if !ok {
            continue
        }
        switch t := v.(type) {
        case float64:
            nums = append(nums, t)
        case int, int32, int64:
            nums = append(nums, float64(asInt64(v)))
        case string:
            if f, ok := tryParseFloat(t); ok {
                nums = append(nums, f)
            } else {
                counts[t]++
            }
        }
    }
    items := []statItem{}
    if len(nums) > 0 {
        uniq := map[float64]int{}
        for _, v := range nums {
            uniq[v]++
        }
        if len(uniq) <= 40 {
            // Treat as categorical numeric exact values
            type kv struct{ k float64; v int }
            arr := make([]kv, 0, len(uniq))
            for k, v := range uniq { arr = append(arr, kv{k, v}) }
            sort.Slice(arr, func(i, j int) bool { return arr[i].v > arr[j].v })
            for _, it := range arr {
                items = append(items, statItem{
                    label:   formatNumericLabel(it.k),
                    count:   it.v,
                    hasExact: true,
                    fvalue:  it.k,
                })
            }
            return items
        }
        // Build bins 5..40 based on distinct count
        uniqCount := len(uniq)
        bins := uniqCount
        if bins < 5 { bins = 5 }
        if bins > 40 { bins = 40 }
        minv, maxv := nums[0], nums[0]
        for _, v := range nums { if v < minv { minv = v }; if v > maxv { maxv = v } }
        hist := make([]int, bins)
        edges := make([]float64, bins+1)
        step := 0.0
        if bins > 0 { step = (maxv - minv) / float64(bins) }
        if maxv == minv || step == 0 {
            hist[0] = len(nums)
            edges[0], edges[1] = minv, maxv
        } else {
            for _, v := range nums {
                idx := int(math.Floor(float64(bins) * (v - minv) / (maxv - minv)))
                if idx >= bins { idx = bins - 1 }
                if idx < 0 { idx = 0 }
                hist[idx]++
            }
            for i := 0; i <= bins; i++ { edges[i] = minv + float64(i)*step }
        }
        for i := 0; i < bins; i++ {
            low := edges[i]
            high := edges[minInt(i+1, len(edges)-1)]
            label := sprintf("[%.2f – %.2f]", low, high)
            items = append(items, statItem{ label: label, count: hist[i], hasRange: true, low: low, high: high })
        }
        return items
    }
    // Categorical strings
    type kv struct{ k string; v int }
    arr := make([]kv, 0, len(counts))
    for k, v := range counts { arr = append(arr, kv{k, v}) }
    sort.Slice(arr, func(i, j int) bool { return arr[i].v > arr[j].v })
    for _, it := range arr { items = append(items, statItem{ label: it.k, svalue: it.k, count: it.v }) }
    return items
}

// renderStatsList paints the stats items with a fixed 50/50 split between
// label and bar columns. The selected item is prefixed with "> ".
func renderStatsList(items []statItem, width, sel int) string {
    if width < 20 { width = 20 }
    // Reserve 2 chars for selection prefix ("  " or "> ")
    usable := width - 2
    if usable < 10 { usable = 10 }
    labelW := usable / 2
    barW := usable - labelW
    // Compute max for scaling
    maxc := 1
    for _, it := range items { if it.count > maxc { maxc = it.count } }
    var b strings.Builder
    for i, it := range items {
        prefix := "  "
        if i == sel { prefix = "> " }
        label := it.label
        if runeLen(label) > labelW { label = truncateRunes(label, labelW) }
        label = padRight(label, labelW)
        cnt := sprintf("(%d)", it.count)
        // Leave space for space + count in the bar area
        countSpace := runeLen(cnt) + 1
        widthBar := barW - countSpace
        if widthBar < 0 { widthBar = 0 }
        scaled := 0
        if maxc > 0 { scaled = int(math.Round(float64(widthBar) * float64(it.count) / float64(maxc))) }
        bar := colorBar(scaled, float64(it.count), float64(maxc))
        // Right-pad bar area to exact barW by adding spaces then count at end
        pad := strings.Repeat(" ", max(0, widthBar-scaled))
        line := sprintf("%s%s%s %s\n", prefix, label, bar+pad, cnt)
        b.WriteString(line)
    }
    return b.String()
}

func runeLen(s string) int { return len([]rune(s)) }
func padRight(s string, w int) string {
    rs := []rune(s)
    if len(rs) >= w { return s }
    return s + strings.Repeat(" ", w-len(rs))
}
func truncateRunes(s string, w int) string {
    rs := []rune(s)
    if len(rs) <= w { return s }
    return string(rs[:w])
}

// buildTimeDistribution builds a vertical bar chart over time for a selected
// stats item (by index) using the provided viewport width/height.
func buildTimeDistribution(field string, items []statItem, sel int, entries []model.LogEntry, width, height int) string {
    if sel < 0 || sel >= len(items) {
        return "No selection"
    }
    it := items[sel]
    // Collect timestamps and matching events
    var minT, maxT int64
    first := true
    for _, e := range entries {
        if e.Timestamp == nil { continue }
        ts := e.Timestamp.Unix()
        if first { minT, maxT = ts, ts; first = false } else {
            if ts < minT { minT = ts }
            if ts > maxT { maxT = ts }
        }
    }
    if first || minT == maxT {
        return "Not enough timestamped data to chart"
    }
    // Use nearly full width for columns
    cols := width - 2
    if cols < 10 { cols = 10 }
    buckets := make([]int, cols)
    rng := float64(maxT-minT) + 1
    match := func(e model.LogEntry) bool {
        v, ok := e.Fields[field]
        if !ok { return false }
        if it.hasRange {
            // parse numeric
            var f float64
            switch t := v.(type) {
            case float64:
                f = t
            case int, int32, int64:
                f = float64(asInt64(v))
            case string:
                fv, ok := tryParseFloat(t)
                if !ok { return false }
                f = fv
            default:
                return false
            }
            // inclusive low, exclusive high except last bucket
            if f < it.low { return false }
            if f > it.high { return false }
            return true
        }
        if it.hasExact {
            var f float64
            switch t := v.(type) {
            case float64:
                f = t
            case int, int32, int64:
                f = float64(asInt64(v))
            case string:
                fv, ok := tryParseFloat(t)
                if !ok { return false }
                f = fv
            default:
                return false
            }
            return f == it.fvalue
        }
        // Categorical string
        s, ok := v.(string)
        if !ok { return false }
        return s == it.svalue
    }
    totalMatches := 0
    for _, e := range entries {
        if e.Timestamp == nil { continue }
        if !match(e) { continue }
        ts := float64(e.Timestamp.Unix()-minT)
        idx := int(math.Floor(float64(cols) * ts / rng))
        if idx < 0 { idx = 0 }
        if idx >= cols { idx = cols - 1 }
        buckets[idx]++
        totalMatches++
    }
    // Determine height for bars
    chartH := height - 6
    if chartH < 3 { chartH = 3 }
    // Prepare vertical bars
    lines := make([]string, chartH)
    maxc := 1
    for _, v := range buckets { if v > maxc { maxc = v } }
    for row := chartH; row >= 1; row-- {
        var sb strings.Builder
        for i := 0; i < cols; i++ {
            h := int(math.Round(float64(buckets[i]) * float64(chartH) / float64(maxc)))
            if h >= row {
                sb.WriteString("▇")
            } else {
                sb.WriteString(" ")
            }
        }
        lines[chartH-row] = sb.String()
    }
    body := strings.Join(lines, "\n")
    // Bottom axis with time labels at left/mid/right
    t0 := time.Unix(minT, 0)
    t1 := time.Unix(maxT, 0)
    mid := time.Unix((minT+maxT)/2, 0)
    left := t0.Format("01-02 15:04:05")
    center := mid.Format("01-02 15:04:05")
    right := t1.Format("01-02 15:04:05")
    axis := placeThree(left, center, right, cols)
    summary := sprintf("count:%d  max/bin:%d", totalMatches, maxc)
    return body + "\n" + axis + "\n" + summary
}

// placeThree places left, center, right labels proportionally across a width.
func placeThree(left, center, right string, width int) string {
    if width < 10 { width = 10 }
    // Truncate labels if needed
    trim := func(s string, w int) string {
        rs := []rune(s)
        if len(rs) <= w { return s }
        return string(rs[:w])
    }
    maxw := width / 3
    if maxw < 8 { maxw = 8 }
    left = trim(left, maxw)
    center = trim(center, maxw)
    right = trim(right, maxw)
    // Build line
    line := make([]rune, width)
    for i := range line { line[i] = ' ' }
    // Place left at 0
    copy(line[0:], []rune(left))
    // Place center approximately in the middle
    midStart := (width - runeLen(center)) / 2
    if midStart < 0 { midStart = 0 }
    copy(line[midStart:], []rune(center))
    // Place right aligned to end
    rstart := width - runeLen(right)
    if rstart < 0 { rstart = 0 }
    copy(line[rstart:], []rune(right))
    return string(line)
}

func tryParseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func asInt64(v any) int64 {
	switch t := v.(type) {
	case int:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	default:
		return 0
	}
}

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
