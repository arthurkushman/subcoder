package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/arthurkushman/subcoder/internal/config"
)

// TestHelperProcess acts as a fake MCP stdio server when spawned by the tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("MCP_HELPER") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	defer writer.Flush()

	for scanner.Scan() {
		var request map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		method, _ := request["method"].(string)
		id, hasID := request["id"]

		var result any
		switch method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echo text back",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
			}}}
		case "tools/call":
			params, _ := request["params"].(map[string]any)
			arguments, _ := params["arguments"].(map[string]any)
			text, _ := arguments["text"].(string)
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "echo:" + text}}}
		default:
			result = map[string]any{}
		}

		if !hasID || id == nil {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
		data, _ := json.Marshal(response)
		fmt.Fprintln(writer, string(data))
		writer.Flush()
	}
	os.Exit(0)
}

func startFakeStdio(t *testing.T) *StdioClient {
	t.Helper()
	client, err := StartStdio(context.Background(), "fake", []string{os.Args[0], "-test.run=TestHelperProcess"}, map[string]string{"MCP_HELPER": "1"})
	if err != nil {
		t.Fatalf("start stdio: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestStdioClientListAndCall(t *testing.T) {
	client := startFakeStdio(t)

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" || tools[0].Description != "Echo text back" {
		t.Fatalf("tools = %+v", tools)
	}

	output, err := client.CallTool(context.Background(), "echo", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if output != "echo:hi" {
		t.Fatalf("output = %q", output)
	}
}

func TestManagerRegistersPrefixedTools(t *testing.T) {
	servers := map[string]config.MCPServerConfig{
		"fake": {Command: []string{os.Args[0], "-test.run=TestHelperProcess"}, Environment: map[string]string{"MCP_HELPER": "1"}},
	}
	manager, err := Start(context.Background(), servers)
	if err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer manager.Close()

	tools := manager.Tools()
	if len(tools) != 1 || tools[0].Name() != "fake_echo" {
		t.Fatalf("tools = %+v", tools)
	}

	result, err := tools[0].Execute(context.Background(), json.RawMessage(`{"text":"yo"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "echo:yo" {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestManagerSkipsDisabledAndReportsFailures(t *testing.T) {
	disabled := false
	servers := map[string]config.MCPServerConfig{
		"off": {Command: []string{"definitely-not-a-command"}, Enabled: &disabled},
	}
	manager, err := Start(context.Background(), servers)
	if err != nil {
		t.Fatalf("disabled server should not error: %v", err)
	}
	if len(manager.Tools()) != 0 {
		t.Fatalf("tools = %+v", manager.Tools())
	}

	servers = map[string]config.MCPServerConfig{
		"broken": {Command: []string{"definitely-not-a-command-123"}},
	}
	manager, err = Start(context.Background(), servers)
	if err == nil {
		t.Fatal("expected failure report")
	}
	if len(manager.Tools()) != 0 {
		t.Fatalf("tools = %+v", manager.Tools())
	}
}

func TestHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Mcp-Session-Id", "test-session")
		method, _ := request["method"].(string)
		id := request["id"]

		var result any
		switch method {
		case "initialize":
			result = map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "ping", "description": "Ping", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "pong"}}}
		default:
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if id == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	}))
	defer server.Close()

	client, err := NewHTTPClient(context.Background(), "remote", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(context.Background())
	if err != nil || len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("tools=%+v err=%v", tools, err)
	}
	output, err := client.CallTool(context.Background(), "ping", json.RawMessage(`{}`))
	if err != nil || output != "pong" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}
