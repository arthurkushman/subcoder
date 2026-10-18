package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arthurkushman/subcoder/internal/provider"
)

func TestStreamTextAndToolUse(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "key-1" {
			t.Fatalf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("version = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatal(err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		events := []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":9,"cache_read_input_tokens":2}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"bash"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"cmd\":"}}`,
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"ls\"}"}}`,
			`{"type":"content_block_stop","index":1}`,
			`{"type":"message_delta","usage":{"output_tokens":12}}`,
			`{"type":"message_stop"}`,
		}
		for _, event := range events {
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", "message", event)
			flusher.Flush()
		}
	}))
	defer server.Close()

	p := New(Options{BaseURL: server.URL, APIKey: "key-1"})
	stream, err := p.Stream(context.Background(), provider.Request{
		Model:  "claude-test",
		System: "system prompt",
		Messages: []provider.ChatMessage{
			{Role: "user", Text: "hello"},
			{Role: "assistant", Text: "let me check", ToolCalls: []provider.ToolCall{{ID: "toolu_0", Name: "read", Arguments: `{"filePath":"/tmp/x"}`}}},
			{Role: "tool", ToolCallID: "toolu_0", ToolName: "read", Text: "file contents"},
			{Role: "tool", ToolCallID: "toolu_0b", ToolName: "read", Text: "more"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text string
	var calls []provider.ToolCall
	var inputTotal, outputTotal float64
	for ev := range stream {
		switch ev.Type {
		case provider.EventText:
			text += ev.Text
		case provider.EventToolCall:
			calls = append(calls, *ev.ToolCall)
		case provider.EventUsage:
			inputTotal += ev.Usage.Input
			outputTotal += ev.Usage.Output
		case provider.EventError:
			t.Fatalf("error: %v", ev.Err)
		}
	}

	if text != "Hi" {
		t.Fatalf("text = %q", text)
	}
	if len(calls) != 1 || calls[0].Name != "bash" || calls[0].Arguments != `{"cmd":"ls"}` {
		t.Fatalf("calls = %+v", calls)
	}
	if inputTotal != 9 || outputTotal != 12 {
		t.Fatalf("usage input=%v output=%v", inputTotal, outputTotal)
	}

	// Verify request conversion.
	if captured["system"] != "system prompt" {
		t.Fatalf("system = %v", captured["system"])
	}
	msgs, ok := captured["messages"].([]any)
	if !ok || len(msgs) != 3 {
		t.Fatalf("messages = %#v", captured["messages"])
	}
	assistant := msgs[1].(map[string]any)
	blocks := assistant["content"].([]any)
	if len(blocks) != 2 || blocks[1].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("assistant blocks = %#v", blocks)
	}
	toolResultMsg := msgs[2].(map[string]any)
	resultBlocks := toolResultMsg["content"].([]any)
	if len(resultBlocks) != 2 {
		t.Fatalf("tool result blocks grouped = %#v", resultBlocks)
	}
	if resultBlocks[0].(map[string]any)["type"] != "tool_result" {
		t.Fatalf("block type = %#v", resultBlocks[0])
	}
}

func TestStreamAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"nope"}}`)
	}))
	defer server.Close()

	p := New(Options{BaseURL: server.URL})
	_, err := p.Stream(context.Background(), provider.Request{Model: "m"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModelsFromAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"claude-x","display_name":"Claude X"}]}`)
	}))
	defer server.Close()

	p := New(Options{BaseURL: server.URL})
	models, err := p.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].Name != "Claude X" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
}
