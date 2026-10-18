package provider

import (
	"context"
	"strings"
	"testing"
)

func TestReadSSE(t *testing.T) {
	input := "event: message\ndata: {\"a\":1}\n\ndata: plain\n\n: comment\ndata: last"

	var events []string
	var datas []string
	err := ReadSSE(context.Background(), strings.NewReader(input), func(event string, data []byte) error {
		events = append(events, event)
		datas = append(datas, string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(datas) != 3 {
		t.Fatalf("datas = %v", datas)
	}
	if events[0] != "message" || datas[0] != `{"a":1}` {
		t.Fatalf("first = %q %q", events[0], datas[0])
	}
	if datas[1] != "plain" || events[1] != "" {
		t.Fatalf("second = %q %q", events[1], datas[1])
	}
	if datas[2] != "last" {
		t.Fatalf("third = %q", datas[2])
	}
}

func TestReadSSEMultiLineData(t *testing.T) {
	input := "data: one\ndata: two\n\n"
	var got string
	if err := ReadSSE(context.Background(), strings.NewReader(input), func(_ string, data []byte) error {
		got = string(data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != "one\ntwo" {
		t.Fatalf("got %q", got)
	}
}

func TestRegistryResolve(t *testing.T) {
	reg := NewRegistry()
	mock := &fakeProvider{id: "mock", models: []Model{{ID: "mock-small", Provider: "mock"}}}
	reg.Register(mock)
	reg.SetDefault("mock/mock-small")

	p, m, err := reg.Resolve(context.Background(), "")
	if err != nil || p.ID() != "mock" || m.ID != "mock-small" {
		t.Fatalf("resolve default: %v %v %v", p, m, err)
	}

	p, m, err = reg.Resolve(context.Background(), "mock/unknown")
	if err != nil || m.ID != "unknown" {
		t.Fatalf("resolve unknown model: %v %v", m, err)
	}

	if _, _, err := reg.Resolve(context.Background(), "nope/x"); err == nil {
		t.Fatal("expected unknown provider error")
	}

	p, m, err = reg.Resolve(context.Background(), "mock-small")
	if err != nil || p.ID() != "mock" || m.ID != "mock-small" {
		t.Fatalf("bare resolve: %v %v", m, err)
	}
}

func TestRegistryModelsFallsBackToBuiltin(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&fakeProvider{id: "anthropic"})
	models := reg.Models(context.Background())
	if len(models) == 0 || models[0].Provider != "anthropic" {
		t.Fatalf("models = %v", models)
	}
}

func TestCost(t *testing.T) {
	m := Model{InputCost: 3, OutputCost: 15}
	cost := Cost(m, Usage{Input: 1_000_000, Output: 1_000_000})
	if cost != 18 {
		t.Fatalf("cost = %v", cost)
	}
}

type fakeProvider struct {
	id     string
	models []Model
}

func (f *fakeProvider) ID() string { return f.id }

func (f *fakeProvider) Models(context.Context) ([]Model, error) { return f.models, nil }

func (f *fakeProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	return Stream(ctx, func(ctx context.Context, emit func(Event) error) error {
		return emit(Event{Type: EventDone})
	}), nil
}
