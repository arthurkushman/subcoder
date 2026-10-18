package mock

import (
	"context"
	"fmt"
	"sync"

	"github.com/arthurkushman/subcoder/internal/provider"
)

type Step struct {
	Text      []string
	Reasoning []string
	ToolCalls []provider.ToolCall
	Usage     *provider.Usage
	Err       error
}

type Provider struct {
	mu       sync.Mutex
	id       string
	models   []provider.Model
	steps    []Step
	requests []provider.Request
}

func New(id string, models ...provider.Model) *Provider {
	if len(models) == 0 {
		models = provider.BuiltinModels["mock"]
		if id != "mock" {
			models = []provider.Model{{ID: id + "-model", Provider: id, Name: id + " model"}}
		}
	}
	return &Provider{id: id, models: models}
}

func (p *Provider) ID() string {
	return p.id
}

func (p *Provider) Models(context.Context) ([]provider.Model, error) {
	return p.models, nil
}

func (p *Provider) AddStep(step Step) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.steps = append(p.steps, step)
}

func (p *Provider) AddText(text string) {
	p.AddStep(Step{Text: []string{text}})
}

func (p *Provider) AddToolCall(name, arguments string) {
	p.AddStep(Step{ToolCalls: []provider.ToolCall{{ID: "call_" + name, Name: name, Arguments: arguments}}})
}

func (p *Provider) Requests() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Request(nil), p.requests...)
}

func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	if len(p.steps) == 0 {
		p.mu.Unlock()
		return nil, fmt.Errorf("mock: no scripted steps left")
	}
	step := p.steps[0]
	p.steps = p.steps[1:]
	p.mu.Unlock()

	return provider.Stream(ctx, func(ctx context.Context, emit func(provider.Event) error) error {
		if step.Err != nil {
			return step.Err
		}
		for _, text := range step.Reasoning {
			if err := emit(provider.Event{Type: provider.EventReasoning, Text: text}); err != nil {
				return err
			}
		}
		for _, text := range step.Text {
			if err := emit(provider.Event{Type: provider.EventText, Text: text}); err != nil {
				return err
			}
		}
		for i := range step.ToolCalls {
			call := step.ToolCalls[i]
			if err := emit(provider.Event{Type: provider.EventToolCall, ToolCall: &call}); err != nil {
				return err
			}
		}
		if step.Usage != nil {
			if err := emit(provider.Event{Type: provider.EventUsage, Usage: step.Usage}); err != nil {
				return err
			}
		}
		return emit(provider.Event{Type: provider.EventDone})
	}), nil
}
