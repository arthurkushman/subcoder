package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arthurkushman/subcoder/internal/provider"
)

func TestStreamTextToolCallAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("auth = %q", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] != true {
			t.Fatalf("stream = %v", body["stream"])
		}
		if tools, ok := body["tools"].([]any); !ok || len(tools) != 1 {
			t.Fatalf("tools = %v", body["tools"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		chunks := []string{
			`{"choices":[{"delta":{"content":"Hel"}}]}`,
			`{"choices":[{"delta":{"reasoning":"think "}}]}`,
			`{"choices":[{"delta":{"content":"lo"}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"bash","arguments":"{\"comm"}}]}}]}`,
			`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]},"finish_reason":"tool_calls"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"completion_tokens_details":{"reasoning_tokens":2},"prompt_tokens_details":{"cached_tokens":3}}}`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	p := New(Options{ID: "openai", BaseURL: server.URL, APIKey: "test-key"})
	stream, err := p.Stream(context.Background(), provider.Request{
		Model: "gpt-test",
		Messages: []provider.ChatMessage{
			{Role: "user", Text: "hi"},
		},
		Tools: []provider.ToolSpec{{Name: "bash", Description: "run", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text, reasoning string
	var calls []provider.ToolCall
	var usage provider.Usage
	done := false
	for ev := range stream {
		switch ev.Type {
		case provider.EventText:
			text += ev.Text
		case provider.EventReasoning:
			reasoning += ev.Text
		case provider.EventToolCall:
			calls = append(calls, *ev.ToolCall)
		case provider.EventUsage:
			usage = *ev.Usage
		case provider.EventDone:
			done = true
		case provider.EventError:
			t.Fatalf("error: %v", ev.Err)
		}
	}

	if text != "Hello" || reasoning != "think " {
		t.Fatalf("text=%q reasoning=%q", text, reasoning)
	}
	if len(calls) != 1 || calls[0].Name != "bash" || calls[0].Arguments != `{"command":"ls"}` {
		t.Fatalf("calls = %+v", calls)
	}
	if usage.Input != 11 || usage.Output != 7 || usage.Reasoning != 2 || usage.CacheRead != 3 {
		t.Fatalf("usage = %+v", usage)
	}
	if !done {
		t.Fatal("missing done event")
	}
}

func TestStreamHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
	}))
	defer server.Close()

	p := New(Options{BaseURL: server.URL})
	_, err := p.Stream(context.Background(), provider.Request{Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}

func TestModelsFromAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"gpt-a"},{"id":"gpt-b"}]}`)
	}))
	defer server.Close()

	p := New(Options{ID: "openai", BaseURL: server.URL})
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "gpt-a" || models[0].Provider != "openai" {
		t.Fatalf("models = %+v", models)
	}
}

func TestModelsFallbackOnError(t *testing.T) {
	p := New(Options{ID: "local", BaseURL: "http://127.0.0.1:1", Models: []provider.Model{{ID: "llama", Provider: "local"}}})
	models, _ := p.Models(context.Background())
	if len(models) != 1 || models[0].ID != "llama" {
		t.Fatalf("models = %+v", models)
	}
}
