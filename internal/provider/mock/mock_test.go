package mock

import (
	"context"
	"testing"

	"github.com/arthurkushman/subcoder/internal/provider"
)

func TestScriptedStream(t *testing.T) {
	p := New("mock")
	p.AddStep(Step{
		Text:      []string{"hello ", "world"},
		ToolCalls: []provider.ToolCall{{ID: "t1", Name: "bash", Arguments: `{"command":"ls"}`}},
		Usage:     &provider.Usage{Input: 10, Output: 5},
	})

	stream, err := p.Stream(context.Background(), provider.Request{Model: "mock-small"})
	if err != nil {
		t.Fatal(err)
	}

	var text string
	var calls int
	var usage provider.Usage
	for ev := range stream {
		switch ev.Type {
		case provider.EventText:
			text += ev.Text
		case provider.EventToolCall:
			calls++
			if ev.ToolCall.Name != "bash" {
				t.Fatalf("call = %+v", ev.ToolCall)
			}
		case provider.EventUsage:
			usage = *ev.Usage
		case provider.EventError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}

	if text != "hello world" || calls != 1 || usage.Input != 10 {
		t.Fatalf("text=%q calls=%d usage=%+v", text, calls, usage)
	}

	requests := p.Requests()
	if len(requests) != 1 || requests[0].Model != "mock-small" {
		t.Fatalf("requests = %+v", requests)
	}

	if _, err := p.Stream(context.Background(), provider.Request{}); err == nil {
		t.Fatal("expected error when steps exhausted")
	}
}

func TestScriptedError(t *testing.T) {
	p := New("mock")
	p.AddStep(Step{Err: context.DeadlineExceeded})

	stream, err := p.Stream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	var sawError bool
	for ev := range stream {
		if ev.Type == provider.EventError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected error event")
	}
}

func TestCustomModels(t *testing.T) {
	p := New("custom", provider.Model{ID: "tiny", Provider: "custom"})
	models, err := p.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "tiny" {
		t.Fatalf("models = %v err=%v", models, err)
	}
}
