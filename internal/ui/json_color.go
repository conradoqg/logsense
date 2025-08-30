package ui

import (
	"fmt"
	"sort"
	"strings"
)

func colorizeJSONRoot(v any, st Styles) string {
	var b strings.Builder
	renderJSON(&b, v, st, 0)
	return b.String()
}

func renderJSON(b *strings.Builder, v any, st Styles, indent int) {
	ind := strings.Repeat("  ", indent)
	switch t := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString(st.JSONPunct.Render("{"))
		if len(keys) > 0 {
			b.WriteString("\n")
		}
		for i, k := range keys {
			b.WriteString(ind)
			b.WriteString("  ")
			// "key":
			b.WriteString(st.JSONKey.Render("\"" + escapeString(k) + "\""))
			b.WriteString(st.JSONPunct.Render(": "))
			renderJSON(b, t[k], st, indent+1)
			if i < len(keys)-1 {
				b.WriteString(st.JSONPunct.Render(","))
			}
			b.WriteString("\n")
		}
		b.WriteString(ind)
		b.WriteString(st.JSONPunct.Render("}"))
	case []interface{}:
		b.WriteString(st.JSONPunct.Render("["))
		if len(t) > 0 {
			b.WriteString("\n")
		}
		for i, it := range t {
			b.WriteString(ind)
			b.WriteString("  ")
			renderJSON(b, it, st, indent+1)
			if i < len(t)-1 {
				b.WriteString(st.JSONPunct.Render(","))
			}
			b.WriteString("\n")
		}
		b.WriteString(ind)
		b.WriteString(st.JSONPunct.Render("]"))
	case string:
		b.WriteString(st.JSONString.Render("\"" + escapeString(t) + "\""))
	case float64, float32, int, int32, int64, uint, uint32, uint64:
		b.WriteString(st.JSONNumber.Render(fmt.Sprint(t)))
	case bool:
		if t {
			b.WriteString(st.JSONBool.Render("true"))
		} else {
			b.WriteString(st.JSONBool.Render("false"))
		}
	case nil:
		b.WriteString(st.JSONNull.Render("null"))
	default:
		// Fallback to string representation
		b.WriteString(st.JSONString.Render(fmt.Sprint(t)))
	}
}

func escapeString(s string) string {
	// Minimal escape for quotes and backslashes; printable control chars omitted for brevity
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
