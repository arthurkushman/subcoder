package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

type Model struct {
	ID         string  `json:"id"`
	Provider   string  `json:"provider"`
	Name       string  `json:"name,omitempty"`
	Context    int     `json:"context,omitempty"`
	MaxOutput  int     `json:"maxOutput,omitempty"`
	InputCost  float64 `json:"inputCost,omitempty"`
	OutputCost float64 `json:"outputCost,omitempty"`
}

func (m Model) FullID() string {
	return m.Provider + "/" + m.ID
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatMessage struct {
	Role       string     `json:"role"`
	Text       string     `json:"text,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallID,omitempty"`
	ToolName   string     `json:"toolName,omitempty"`
}

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Request struct {
	Model       string
	System      string
	Messages    []ChatMessage
	Tools       []ToolSpec
	Temperature *float64
	MaxTokens   int
}

type EventType string

const (
	EventText      EventType = "text_delta"
	EventReasoning EventType = "reasoning_delta"
	EventToolCall  EventType = "tool_call"
	EventUsage     EventType = "usage"
	EventDone      EventType = "done"
	EventError     EventType = "error"
)

type Usage struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	Reasoning  float64 `json:"reasoning,omitempty"`
	CacheRead  float64 `json:"cacheRead,omitempty"`
	CacheWrite float64 `json:"cacheWrite,omitempty"`
}

type Event struct {
	Type     EventType `json:"type"`
	Text     string    `json:"text,omitempty"`
	ToolCall *ToolCall `json:"toolCall,omitempty"`
	Usage    *Usage    `json:"usage,omitempty"`
	Err      error     `json:"-"`
}

func (e Event) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

type Provider interface {
	ID() string
	Models(ctx context.Context) ([]Model, error)
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}

// Cost returns the USD cost of a usage report for a model.
func Cost(model Model, usage Usage) float64 {
	return (usage.Input*model.InputCost + usage.Output*model.OutputCost) / 1_000_000
}

type Registry struct {
	mu            sync.RWMutex
	providers     map[string]Provider
	order         []string
	defaultModel  string
	modelsCache   map[string][]Model
	modelsFetched map[string]bool
}

func NewRegistry() *Registry {
	return &Registry{
		providers:     map[string]Provider{},
		modelsCache:   map[string][]Model{},
		modelsFetched: map[string]bool{},
	}
}

func (r *Registry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.providers[p.ID()]; !ok {
		r.order = append(r.order, p.ID())
	}
	r.providers[p.ID()] = p
}

func (r *Registry) Get(id string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	return p, ok
}

func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.order...)
}

func (r *Registry) SetDefault(model string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.defaultModel = model
}

func (r *Registry) Default() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultModel
}

// Models aggregates the model catalogs of all providers. Providers listing
// errors fall back to the static built-in catalog.
func (r *Registry) Models(ctx context.Context) []Model {
	r.mu.RLock()
	providers := make([]Provider, 0, len(r.order))
	for _, id := range r.order {
		providers = append(providers, r.providers[id])
	}
	r.mu.RUnlock()

	var models []Model
	for _, p := range providers {
		list, err := p.Models(ctx)
		if err != nil || len(list) == 0 {
			list = BuiltinModels[p.ID()]
		}
		models = append(models, list...)
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Provider != models[j].Provider {
			return models[i].Provider < models[j].Provider
		}
		return models[i].ID < models[j].ID
	})
	return models
}

// Resolve parses "provider/model" identifiers, or searches all providers for a
// bare model ID.
func (r *Registry) Resolve(ctx context.Context, model string) (Provider, Model, error) {
	if model == "" {
		model = r.Default()
	}
	if model == "" {
		return nil, Model{}, errors.New("provider: no model specified")
	}

	if idx := strings.Index(model, "/"); idx > 0 {
		providerID := model[:idx]
		modelID := model[idx+1:]
		p, ok := r.Get(providerID)
		if !ok {
			return nil, Model{}, fmt.Errorf("provider: unknown provider %q", providerID)
		}
		return p, r.findModel(ctx, p, modelID), nil
	}

	for _, providerID := range r.IDs() {
		p, _ := r.Get(providerID)
		models, err := p.Models(ctx)
		if err != nil || len(models) == 0 {
			models = BuiltinModels[providerID]
		}
		for _, m := range models {
			if m.ID == model {
				return p, m, nil
			}
		}
	}

	return nil, Model{}, fmt.Errorf("provider: model %q not found", model)
}

func (r *Registry) findModel(ctx context.Context, p Provider, modelID string) Model {
	models, err := p.Models(ctx)
	if err != nil || len(models) == 0 {
		models = BuiltinModels[p.ID()]
	}
	for _, m := range models {
		if m.ID == modelID {
			return m
		}
	}
	return Model{ID: modelID, Provider: p.ID(), Name: modelID}
}

var BuiltinModels = map[string][]Model{
	"anthropic": {
		{ID: "claude-opus-4-6", Provider: "anthropic", Name: "Claude Opus 4.6", Context: 200000, MaxOutput: 32000, InputCost: 5, OutputCost: 25},
		{ID: "claude-sonnet-4-5", Provider: "anthropic", Name: "Claude Sonnet 4.5", Context: 200000, MaxOutput: 64000, InputCost: 3, OutputCost: 15},
		{ID: "claude-haiku-4-5", Provider: "anthropic", Name: "Claude Haiku 4.5", Context: 200000, MaxOutput: 64000, InputCost: 1, OutputCost: 5},
	},
	"openai": {
		{ID: "gpt-5.2", Provider: "openai", Name: "GPT-5.2", Context: 400000, MaxOutput: 128000, InputCost: 1.25, OutputCost: 10},
		{ID: "gpt-5.2-mini", Provider: "openai", Name: "GPT-5.2 mini", Context: 400000, MaxOutput: 128000, InputCost: 0.25, OutputCost: 2},
		{ID: "o4-mini", Provider: "openai", Name: "o4-mini", Context: 200000, MaxOutput: 100000, InputCost: 1.1, OutputCost: 4.4},
	},
	"mock": {
		{ID: "mock-small", Provider: "mock", Name: "Mock Small", Context: 128000, MaxOutput: 8192},
		{ID: "mock-large", Provider: "mock", Name: "Mock Large", Context: 256000, MaxOutput: 16384},
	},
}

// ReadSSE parses a text/event-stream stream, invoking handle for every
// dispatched event.
func ReadSSE(ctx context.Context, r io.Reader, handle func(event string, data []byte) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)

	var event string
	var data []byte

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			if len(data) > 0 {
				if err := handle(event, data); err != nil {
					return err
				}
			}
			event, data = "", nil
			continue
		}
		if line[0] == ':' {
			continue
		}
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			event = strings.TrimSpace(string(line[len("event:"):]))
		case bytes.HasPrefix(line, []byte("data:")):
			chunk := bytes.TrimSpace(line[len("data:"):])
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, chunk...)
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	if len(data) > 0 {
		return handle(event, data)
	}
	return nil
}

// Stream returns a channel that delivers events from the given function,
// closing it when iteration finishes.
func Stream(ctx context.Context, produce func(ctx context.Context, emit func(Event) error) error) <-chan Event {
	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		err := produce(ctx, func(ev Event) error {
			select {
			case ch <- ev:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err != nil && ctx.Err() == nil {
			select {
			case ch <- Event{Type: EventError, Err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return ch
}
