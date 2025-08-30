package ai

import (
	"context"
	"encoding/json"
	"errors"
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
	if resp, err := c.callAlt(ctx2, prompt); err == nil {
		if err := json.Unmarshal([]byte(resp), &out); err == nil {
			return toSchema(out), nil
		}
	}
	return model.Schema{}, errors.New("failed to infer schema")
}

func (c *OpenAIClient) callAlt(ctx context.Context, prompt string) (string, error) {
	cfg := altai.DefaultConfig(c.apiKey)
	if c.baseURL != "" {
		cfg.BaseURL = c.baseURL
	}
	cli := altai.NewClientWithConfig(cfg)
	resp, err := cli.CreateChatCompletion(ctx, altai.ChatCompletionRequest{
		Model: c.model,
		Messages: []altai.ChatCompletionMessage{
			{Role: altai.ChatMessageRoleSystem, Content: "You detect log formats and return ONLY strict JSON following the specified contract. No prose, no code fences."},
			{Role: altai.ChatMessageRoleUser, Content: prompt},
		},
		Temperature:    0.2,
		ResponseFormat: &altai.ChatCompletionResponseFormat{Type: altai.ChatCompletionResponseFormatTypeJSONObject},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("empty choices")
	}
	return resp.Choices[0].Message.Content, nil
}

func buildSchemaPrompt(lines []string) string {
	// Limit to 200 lines
	max := 200
	if len(lines) < max {
		max = len(lines)
	}
	var b strings.Builder
	b.WriteString("Analyze the log lines below and return ONLY strict JSON matching this contract: ")
	b.WriteString("{formatName, probableSources, parseStrategy, timeLayout, levelMapping, schema:{fields:[{name,type,description,pathOrGroup}]}, regexPattern, confidence, sampleParsedRow}.\n")
	b.WriteString("Lines:\n")
	for i := 0; i < max; i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	return b.String()
}

func toSchema(a aiResponse) model.Schema {
	return model.Schema{FormatName: a.FormatName, ProbableSources: a.ProbableSources, ParseStrategy: a.ParseStrategy, TimeLayout: a.TimeLayout, LevelMapping: a.LevelMapping, RegexPattern: a.RegexPattern, Fields: a.Schema.Fields, Confidence: a.Confidence, SampleParsedRow: a.SampleParsedRow}
}
