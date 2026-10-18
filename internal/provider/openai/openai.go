package openai

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

type Options struct {
	ID      string
	BaseURL string
	APIKey  string
	Models  []provider.Model
	Headers map[string]string
}

type Provider struct {
	id      string
	baseURL string
	apiKey  string
	models  []provider.Model
	headers map[string]string
	client  *http.Client
}

func New(opts Options) *Provider {
	id := opts.ID
	if id == "" {
		id = "openai"
	}
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &Provider{
		id:      id,
		baseURL: baseURL,
		apiKey:  opts.APIKey,
		models:  opts.Models,
		headers: opts.Headers,
		client:  &http.Client{},
	}
}

func (p *Provider) ID() string {
	return p.id
}

func (p *Provider) Models(ctx context.Context) ([]provider.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
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
			ID string `json:"id"`
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
		models = append(models, provider.Model{ID: item.ID, Provider: p.id, Name: item.ID})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })

	if len(models) == 0 {
		models = p.models
	}
	return models, nil
}

type chatRequest struct {
	Model         string        `json:"model"`
	Messages      []chatMessage `json:"messages"`
	Tools         []chatTool    `json:"tools,omitempty"`
	Stream        bool          `json:"stream"`
	StreamOptions *streamOpts   `json:"stream_options,omitempty"`
	Temperature   *float64      `json:"temperature,omitempty"`
	MaxTokens     int           `json:"max_tokens,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	body := chatRequest{
		Model:         req.Model,
		Stream:        true,
		StreamOptions: &streamOpts{IncludeUsage: true},
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
	}
	if req.System != "" {
		body.Messages = append(body.Messages, chatMessage{Role: "system", Content: req.System})
	}
	for _, msg := range req.Messages {
		converted := chatMessage{
			Role:       msg.Role,
			Content:    msg.Text,
			ToolCallID: msg.ToolCallID,
		}
		for _, call := range msg.ToolCalls {
			tc := chatToolCall{ID: call.ID, Type: "function"}
			tc.Function.Name = call.Name
			tc.Function.Arguments = call.Arguments
			converted.ToolCalls = append(converted.ToolCalls, tc)
		}
		body.Messages = append(body.Messages, converted)
	}
	for _, spec := range req.Tools {
		body.Tools = append(body.Tools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        spec.Name,
				Description: spec.Description,
				Parameters:  spec.Parameters,
			},
		})
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	p.authorize(httpReq)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}

	return provider.Stream(ctx, func(ctx context.Context, emit func(provider.Event) error) error {
		defer resp.Body.Close()
		return p.consume(ctx, resp.Body, emit)
	}), nil
}

func (p *Provider) consume(ctx context.Context, body io.Reader, emit func(provider.Event) error) error {
	type accumulatingCall struct {
		id   string
		name string
		args strings.Builder
	}
	calls := map[int]*accumulatingCall{}
	var order []int
	emitted := false

	emitCalls := func() error {
		for _, idx := range order {
			call := calls[idx]
			if call.name == "" {
				continue
			}
			emitted = true
			toolCall := provider.ToolCall{ID: call.id, Name: call.name, Arguments: call.args.String()}
			if toolCall.Arguments == "" {
				toolCall.Arguments = "{}"
			}
			if err := emit(provider.Event{Type: provider.EventToolCall, ToolCall: &toolCall}); err != nil {
				return err
			}
		}
		return nil
	}

	err := provider.ReadSSE(ctx, body, func(_ string, data []byte) error {
		if string(data) == "[DONE]" {
			return nil
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens            int `json:"prompt_tokens"`
				CompletionTokens        int `json:"completion_tokens"`
				CompletionTokensDetails struct {
					ReasoningTokens int `json:"reasoning_tokens"`
				} `json:"completion_tokens_details"`
				PromptTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil
		}

		for _, choice := range chunk.Choices {
			delta := choice.Delta
			text := delta.Content
			if text != "" {
				if err := emit(provider.Event{Type: provider.EventText, Text: text}); err != nil {
					return err
				}
			}
			reasoning := delta.ReasoningContent
			if reasoning == "" {
				reasoning = delta.Reasoning
			}
			if reasoning != "" {
				if err := emit(provider.Event{Type: provider.EventReasoning, Text: reasoning}); err != nil {
					return err
				}
			}
			for _, tc := range delta.ToolCalls {
				call, ok := calls[tc.Index]
				if !ok {
					call = &accumulatingCall{}
					calls[tc.Index] = call
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					call.id = tc.ID
				}
				if tc.Function.Name != "" {
					call.name = tc.Function.Name
				}
				call.args.WriteString(tc.Function.Arguments)
			}
			if choice.FinishReason != nil && *choice.FinishReason == "tool_calls" {
				if err := emitCalls(); err != nil {
					return err
				}
			}
		}

		if chunk.Usage != nil {
			usage := provider.Usage{
				Input:     float64(chunk.Usage.PromptTokens),
				Output:    float64(chunk.Usage.CompletionTokens),
				Reasoning: float64(chunk.Usage.CompletionTokensDetails.ReasoningTokens),
				CacheRead: float64(chunk.Usage.PromptTokensDetails.CachedTokens),
			}
			if err := emit(provider.Event{Type: provider.EventUsage, Usage: &usage}); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	if !emitted && len(order) > 0 {
		if err := emitCalls(); err != nil {
			return err
		}
	}

	return emit(provider.Event{Type: provider.EventDone})
}

func (p *Provider) authorize(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	for key, value := range p.headers {
		req.Header.Set(key, value)
	}
}
