package ui

import (
	"encoding/base64"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

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
		if len(oLines[i]) > 0 {
			out[i] = oLines[i]
		} else {
			out[i] = bLines[i]
		}
	}
	return strings.Join(out, "\n")
}

// copyToClipboard tries to copy text using OSC52 (works in many terminals).
func copyToClipboard(s string) {
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
