package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	altai "github.com/sashabaranov/go-openai"

	"logsense/internal/model"
)

// Minimal client wrapper that can use official or alternative library.
type OpenAIClient struct {
	apiKey  string
	baseURL string
	model   string
	timeout time.Duration
}

func NewOpenAIClient(apiKey, baseURL, model string, timeout time.Duration) *OpenAIClient {
	return &OpenAIClient{apiKey: apiKey, baseURL: baseURL, model: model, timeout: timeout}
}

type aiResponse struct {
	FormatName      string            `json:"formatName"`
	ProbableSources []string          `json:"probableSources"`
	ParseStrategy   string            `json:"parseStrategy"`
	TimeLayout      string            `json:"timeLayout"`
	LevelMapping    map[string]string `json:"levelMapping"`
	Schema          struct {
		Fields []model.FieldDef `json:"fields"`
	} `json:"schema"`
	RegexPattern    string         `json:"regexPattern"`
	Confidence      float64        `json:"confidence"`
	SampleParsedRow map[string]any `json:"sampleParsedRow"`
}

func (c *OpenAIClient) InferSchema(ctx context.Context, lines []string) (model.Schema, error) {
	if c == nil || c.apiKey == "" {
		return model.Schema{}, errors.New("openai disabled")
	}
	prompt := buildSchemaPrompt(lines)
	ctx2, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var out aiResponse
	resp, err := c.callAlt(ctx2, prompt)
	if err != nil {
		return model.Schema{}, err
	}
	// Try direct unmarshal
	if uerr := json.Unmarshal([]byte(resp), &out); uerr == nil {
		return toSchema(out), nil
	} else {
		// Try to extract JSON substring (in case of stray prose or code fences)
		start := strings.Index(resp, "{")
		end := strings.LastIndex(resp, "}")
		if start >= 0 && end > start {
			body := resp[start : end+1]
			if uerr2 := json.Unmarshal([]byte(body), &out); uerr2 == nil {
				return toSchema(out), nil
			}
		}
		// Surface parsing error and a snippet for debugging
		snippet := resp
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return model.Schema{}, fmt.Errorf("invalid JSON from OpenAI: %v; content: %s", uerr, snippet)
	}
}

func (c *OpenAIClient) callAlt(ctx context.Context, prompt string) (string, error) {
	cfg := altai.DefaultConfig(c.apiKey)
	if c.baseURL != "" {
		cfg.BaseURL = c.baseURL
	}
	cli := altai.NewClientWithConfig(cfg)
    // Build request and adjust options for model quirks (e.g., gpt-5)
    req := altai.ChatCompletionRequest{
        Model: c.model,
        Messages: []altai.ChatCompletionMessage{
            {Role: altai.ChatMessageRoleSystem, Content: "You detect log formats and return ONLY strict JSON following the specified contract. No prose, no code fences."},
            {Role: altai.ChatMessageRoleUser, Content: prompt},
        },
        ResponseFormat: &altai.ChatCompletionResponseFormat{Type: altai.ChatCompletionResponseFormatTypeJSONObject},
    }
    if strings.HasPrefix(strings.ToLower(c.model), "gpt-5") {
        // Some models only support default temperature (1)
        req.Temperature = 1
    } else {
        req.Temperature = 0.2
    }
    resp, err := cli.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("empty choices")
	}
	return resp.Choices[0].Message.Content, nil
}

// ExplainLog asks the model to explain a single log entry and suggest fixes if it's an error.
func (c *OpenAIClient) ExplainLog(ctx context.Context, raw string, fields map[string]any) (string, error) {
    if c == nil || c.apiKey == "" {
        return "", errors.New("openai disabled")
    }
    cfg := altai.DefaultConfig(c.apiKey)
    if c.baseURL != "" {
        cfg.BaseURL = c.baseURL
    }
    cli := altai.NewClientWithConfig(cfg)
    // Build a concise prompt including raw and parsed fields
    var b strings.Builder
    b.WriteString("Raw log:\n")
    b.WriteString(raw)
    b.WriteString("\n\nParsed fields (JSON):\n")
    if fields != nil {
        if js, err := json.MarshalIndent(fields, "", "  "); err == nil {
            b.Write(js)
        }
    }
    user := b.String()
    sys := "You are a helpful logs assistant. Explain what this single log entry means, likely root cause, impact, and if it is an error, specific steps to fix it. Keep it concise and actionable."
    req := altai.ChatCompletionRequest{
        Model: c.model,
        Messages: []altai.ChatCompletionMessage{
            {Role: altai.ChatMessageRoleSystem, Content: sys},
            {Role: altai.ChatMessageRoleUser, Content: user},
        },
    }
    if strings.HasPrefix(strings.ToLower(c.model), "gpt-5") {
        req.Temperature = 1
    } else {
        req.Temperature = 0.2
    }
    ctx2, cancel := context.WithTimeout(ctx, c.timeout)
    defer cancel()
    resp, err := cli.CreateChatCompletion(ctx2, req)
    if err != nil {
        return "", err
    }
    if len(resp.Choices) == 0 {
        return "", errors.New("empty choices")
    }
    return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func buildSchemaPrompt(lines []string) string {
	// Limit to 200 lines
	max := 200
	if len(lines) < max {
		max = len(lines)
	}
	var b strings.Builder
	// Strong, explicit contract for outputs and constraints tailored to our parser
	b.WriteString("You are a log schema detector. Analyze the sample lines and output ONLY strict JSON without markdown or code fences.\n")
	b.WriteString("Return an object with this exact shape: ")
	b.WriteString("{formatName, probableSources, parseStrategy, timeLayout, levelMapping, schema:{fields:[{name,type,description,pathOrGroup}]}, regexPattern, confidence, sampleParsedRow}.\n\n")
	b.WriteString("Requirements:\n")
	b.WriteString("- parseStrategy: one of [json, logfmt, kv, regex].\n")
	b.WriteString("- If parseStrategy=regex: regexPattern MUST be a single Go RE2-compatible pattern that matches a full line.\n")
	b.WriteString("  - Use named capture groups with Go syntax (?P<name>...) for each field in schema.fields.\n")
	b.WriteString("  - Avoid lookarounds, backreferences, inline flags outside the pattern, or unsupported syntax.\n")
	b.WriteString("  - Prefer anchoring the whole line with ^ and $ when possible.\n")
	b.WriteString("- timeLayout: RFC3339 if timestamps are ISO-8601; otherwise provide the exact Go time layout for the captured timestamp.\n")
	b.WriteString("- levelMapping: map common lowercase keys to canonical levels (TRACE, DEBUG, INFO, WARN, ERROR, FATAL) when applicable; empty if not applicable.\n")
	b.WriteString("- schema.fields: pick meaningful fields actually present in the data. Favor common names when applicable:\n")
	b.WriteString("  [ts|time|timestamp], [level|lvl|severity], [source|component], [msg|message], plus domain fields like ip, method, path, status, bytes, duration, referer, ua.\n")
	b.WriteString("- sampleParsedRow: an example object using the exact field names you defined, with plausible values from the sample.\n")
	b.WriteString("- confidence: 0.0–1.0.\n")
	b.WriteString("- Output JSON only. No prose, no backticks, no markdown.\n\n")
	b.WriteString("Lines:\n")
	for i := 0; i < max; i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	return b.String()
}

func toSchema(a aiResponse) model.Schema {
	// Normalize parseStrategy into known set
	ps := strings.ToLower(strings.TrimSpace(a.ParseStrategy))
	switch {
	case strings.HasPrefix(ps, "json"):
		ps = "json"
	case strings.HasPrefix(ps, "logfmt") || strings.HasPrefix(ps, "kv"):
		ps = "logfmt"
	case strings.HasPrefix(ps, "regex"):
		ps = "regex"
	case ps == "" && a.RegexPattern != "":
		ps = "regex"
	}
	return model.Schema{FormatName: a.FormatName, ProbableSources: a.ProbableSources, ParseStrategy: ps, TimeLayout: a.TimeLayout, LevelMapping: a.LevelMapping, RegexPattern: a.RegexPattern, Fields: a.Schema.Fields, Confidence: a.Confidence, SampleParsedRow: a.SampleParsedRow}
}
