package filter

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Knetic/govaluate"

	"logsense/internal/model"
)

type Criteria struct {
	Query    string // plain contains or regex if /.../
	UseRegex bool
	Levels   map[string]bool
	Expr     string // govaluate expression
	Field    string // when set, apply Query only to this field
}

type Evaluator struct {
	re   *regexp.Regexp
	expr *govaluate.EvaluableExpression
}

func NewEvaluator(c Criteria) (*Evaluator, error) {
	var re *regexp.Regexp
	var expr *govaluate.EvaluableExpression
	var err error
	if c.UseRegex && c.Query != "" {
		re, err = regexp.Compile(c.Query)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(c.Expr) != "" {
		expr, err = govaluate.NewEvaluableExpression(c.Expr)
		if err != nil {
			return nil, err
		}
	}
	return &Evaluator{re: re, expr: expr}, nil
}

func (e *Evaluator) Match(entry model.LogEntry, c Criteria) bool {
	// Level filter
	if len(c.Levels) > 0 {
		if !c.Levels[strings.ToUpper(entry.Level)] {
			return false
		}
	}
	// Query
	if c.Query != "" {
		text := entry.Raw
		if c.Field != "" {
			if v, ok := entry.Fields[c.Field]; ok {
				switch t := v.(type) {
				case string:
					text = t
				default:
					b, _ := json.Marshal(v)
					text = string(b)
				}
			} else {
				text = ""
			}
		}
		if e.re != nil {
			if !e.re.MatchString(text) {
				return false
			}
		} else {
			if !strings.Contains(strings.ToLower(text), strings.ToLower(c.Query)) {
				return false
			}
		}
	}
	if e.expr != nil {
		params := map[string]any{}
		for k, v := range entry.Fields {
			params[k] = v
		}
		// also add common fields
		params["level"] = entry.Level
		if entry.Timestamp != nil {
			params["ts"] = entry.Timestamp.Format("2006-01-02T15:04:05Z07:00")
		}
		result, err := e.expr.Evaluate(params)
		if err != nil {
			return false
		}
		b, ok := result.(bool)
		if !ok || !b {
			return false
		}
	}
	return true
}
