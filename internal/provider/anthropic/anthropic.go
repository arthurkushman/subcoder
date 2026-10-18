package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/arthurkushman/subcoder/internal/provider"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	defaultVersion   = "2023-06-01"
	defaultMaxTokens = 8192
)

type Options struct {
	ID      string
	BaseURL string
	APIKey  string
	Version string
	Models  []provider.Model
}

type Provider struct {
	id      string
	baseURL string
	apiKey  string
	version string
	models  []provider.Model
	client  *http.Client
}

func New(opts Options) *Provider {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	version := opts.Version
	if version == "" {
		version = defaultVersion
	}
	id := opts.ID
	if id == "" {
		id = "anthropic"
	}
	return &Provider{
		id:      id,
		baseURL: baseURL,
		apiKey:  opts.APIKey,
		version: version,
		models:  opts.Models,
		client:  &http.Client{},
	}
}

func (p *Provider) ID() string {
	return p.id
}

func (p *Provider) Models(ctx context.Context) ([]provider.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/v1/models?limit=100", nil)
	if err != nil {
		return nil, err
	}
	p.authorize(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return p.models, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return p.models, nil
	}

	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return p.models, nil
	}

	known := map[string]provider.Model{}
	for _, m := range p.models {
		known[m.ID] = m
	}

	var models []provider.Model
	for _, item := range payload.Data {
		if m, ok := known[item.ID]; ok {
			models = append(models, m)
			continue
		}
		name := item.DisplayName
		if name == "" {
			name = item.ID
		}
		models = append(models, provider.Model{ID: item.ID, Provider: p.id, Name: name})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })

	if len(models) == 0 {
		models = p.models
	}
	return models, nil
}

type message struct {
	Role    string `json:"role"`
	Content []any  `json:"content"`
}

type messagesRequest struct {
	Model       string        `json:"model"`
	System      string        `json:"system,omitempty"`
	Messages    []message     `json:"messages"`
	Tools       []toolPayload `json:"tools,omitempty"`
	Stream      bool          `json:"stream"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens"`
}

type toolPayload struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}

	body := messagesRequest{
		Model:       req.Model,
		System:      req.System,
		Messages:    convertMessages(req.Messages),
		Stream:      true,
		Temperature: req.Temperature,
		MaxTokens:   maxTokens,
	}
	for _, spec := range req.Tools {
		schema := spec.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		body.Tools = append(body.Tools, toolPayload{Name: spec.Name, Description: spec.Description, InputSchema: schema})
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", p.version)
	p.authorize(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("anthropic: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	return provider.Stream(ctx, func(ctx context.Context, emit func(provider.Event) error) error {
		defer resp.Body.Close()
		return p.consume(ctx, resp.Body, emit)
	}), nil
}

func (p *Provider) consume(ctx context.Context, body io.Reader, emit func(provider.Event) error) error {
	type block struct {
		kind    string
		id      string
		name    string
		partial strings.Builder
	}
	blocks := map[int]*block{}

	err := provider.ReadSSE(ctx, body, func(_ string, data []byte) error {
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return nil
		}

		switch envelope.Type {
		case "message_start":
			var event struct {
				Message struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
						CacheRead   int `json:"cache_read_input_tokens"`
						CacheWrite  int `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				return nil
			}
			usage := provider.Usage{
				Input:      float64(event.Message.Usage.InputTokens),
				CacheRead:  float64(event.Message.Usage.CacheRead),
				CacheWrite: float64(event.Message.Usage.CacheWrite),
			}
			if usage.Input > 0 || usage.CacheRead > 0 || usage.CacheWrite > 0 {
				return emit(provider.Event{Type: provider.EventUsage, Usage: &usage})
			}
		case "content_block_start":
			var event struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				return nil
			}
			blocks[event.Index] = &block{kind: event.ContentBlock.Type, id: event.ContentBlock.ID, name: event.ContentBlock.Name}
		case "content_block_delta":
			var event struct {
				Index int `json:"index"`
				Delta struct {
					Type        string `json:"type"`
					Text        string `json:"text"`
					Thinking    string `json:"thinking"`
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				return nil
			}
			current := blocks[event.Index]
			if current == nil {
				current = &block{}
				blocks[event.Index] = current
			}
			switch event.Delta.Type {
			case "text_delta":
				if event.Delta.Text != "" {
					return emit(provider.Event{Type: provider.EventText, Text: event.Delta.Text})
				}
			case "thinking_delta":
				if event.Delta.Thinking != "" {
					return emit(provider.Event{Type: provider.EventReasoning, Text: event.Delta.Thinking})
				}
			case "input_json_delta":
				current.partial.WriteString(event.Delta.PartialJSON)
			}
		case "content_block_stop":
			var event struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				return nil
			}
			current := blocks[event.Index]
			if current != nil && current.kind == "tool_use" && current.name != "" {
				args := current.partial.String()
				if args == "" {
					args = "{}"
				}
				call := provider.ToolCall{ID: current.id, Name: current.name, Arguments: args}
				return emit(provider.Event{Type: provider.EventToolCall, ToolCall: &call})
			}
		case "message_delta":
			var event struct {
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(data, &event); err != nil {
				return nil
			}
			if event.Usage.OutputTokens > 0 {
				usage := provider.Usage{Output: float64(event.Usage.OutputTokens)}
				return emit(provider.Event{Type: provider.EventUsage, Usage: &usage})
			}
		case "error":
			var event struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(data, &event)
			return fmt.Errorf("anthropic: %s: %s", event.Error.Type, event.Error.Message)
		}

		return nil
	})
	if err != nil {
		return err
	}

	return emit(provider.Event{Type: provider.EventDone})
}

func convertMessages(messages []provider.ChatMessage) []message {
	var out []message

	appendBlocks := func(role string, contents ...any) {
		if len(contents) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, contents...)
			return
		}
		out = append(out, message{Role: role, Content: contents})
	}

	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			var contents []any
			if msg.Text != "" {
				contents = append(contents, map[string]any{"type": "text", "text": msg.Text})
			}
			for _, call := range msg.ToolCalls {
				var input any
				if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil || input == nil {
					input = map[string]any{}
				}
				contents = append(contents, map[string]any{
					"type":  "tool_use",
					"id":    call.ID,
					"name":  call.Name,
					"input": input,
				})
			}
			if len(contents) == 0 {
				continue
			}
			appendBlocks("assistant", contents...)
		case "tool":
			appendBlocks("user", map[string]any{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     msg.Text,
			})
		default:
			text := msg.Text
			if text == "" {
				text = " "
			}
			appendBlocks("user", map[string]any{"type": "text", "text": text})
		}
	}

	if len(out) == 0 {
		out = append(out, message{Role: "user", Content: []any{map[string]any{"type": "text", "text": " "}}})
	}
	return out
}

func (p *Provider) authorize(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("x-api-key", p.apiKey)
	}
}
