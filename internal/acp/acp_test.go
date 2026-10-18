package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/arthurkushman/subcoder/internal/agent"
	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/provider/mock"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/arthurkushman/subcoder/internal/tool"
)

type acpHarness struct {
	in      *io.PipeWriter
	out     *bufio.Scanner
	done    chan error
	cancel  context.CancelFunc
	mock    *mock.Provider
	encoder *json.Encoder
}

func newACPHarness(t *testing.T, autoApprove bool) *acpHarness {
	t.Helper()

	dir := t.TempDir()
	store := session.NewStore(t.TempDir())
	mockProvider := mock.New("mock")
	providers := provider.NewRegistry()
	providers.Register(mockProvider)
	providers.SetDefault("mock/mock-small")

	engine := permission.New(nil, nil, autoApprove)
	runner := agent.NewRunner(providers, nil, store, engine)
	runner.Agents = agent.Builtins()
	runner.DefaultModel = "mock/mock-small"
	runner.Tools = tool.NewRegistry(tool.NewBash(), tool.NewRead(), tool.NewWrite())

	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{
			Runner:     runner,
			Sessions:   store,
			Agents:     agent.Builtins(),
			Permission: engine,
			Directory:  dir,
			Model:      "mock/mock-small",
			In:         inReader,
			Out:        outWriter,
		})
	}()

	return &acpHarness{
		in:      inWriter,
		out:     bufio.NewScanner(outReader),
		done:    done,
		cancel:  cancel,
		mock:    mockProvider,
		encoder: json.NewEncoder(inWriter),
	}
}

func (h *acpHarness) send(t *testing.T, message map[string]any) {
	t.Helper()
	if err := h.encoder.Encode(message); err != nil {
		t.Fatal(err)
	}
}

func (h *acpHarness) read(t *testing.T) map[string]any {
	t.Helper()
	if !h.out.Scan() {
		t.Fatalf("no response: %v", h.out.Err())
	}
	var message map[string]any
	if err := json.Unmarshal(h.out.Bytes(), &message); err != nil {
		t.Fatalf("invalid response %q: %v", h.out.Text(), err)
	}
	return message
}

func (h *acpHarness) readUntil(t *testing.T, match func(map[string]any) bool) map[string]any {
	t.Helper()
	for i := 0; i < 100; i++ {
		message := h.read(t)
		if match(message) {
			return message
		}
	}
	t.Fatal("response not found")
	return nil
}

func (h *acpHarness) close(t *testing.T) {
	t.Helper()
	h.in.Close()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for acp server")
	}
	h.cancel()
}

func TestInitializeNewSessionAndPrompt(t *testing.T) {
	h := newACPHarness(t, true)
	h.mock.AddText("hello from acp")

	h.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	init := h.readUntil(t, func(m map[string]any) bool { return m["id"] == float64(1) })
	result := init["result"].(map[string]any)
	if result["protocolVersion"] != float64(ProtocolVersion) {
		t.Fatalf("init = %+v", init)
	}

	h.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": t.TempDir()}})
	newSession := h.readUntil(t, func(m map[string]any) bool { return m["id"] == float64(2) })
	sessionID := newSession["result"].(map[string]any)["sessionId"].(string)
	if !strings.HasPrefix(sessionID, "ses_") {
		t.Fatalf("session = %+v", newSession)
	}

	h.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []any{map[string]any{"type": "text", "text": "hi"}},
		},
	})

	sawText := false
	for {
		message := h.read(t)
		if message["method"] == "session/update" {
			params := message["params"].(map[string]any)
			update := params["update"].(map[string]any)
			if update["sessionUpdate"] == "agent_message_chunk" {
				content := update["content"].(map[string]any)
				if content["text"] == "hello from acp" {
					sawText = true
				}
			}
			continue
		}
		if message["id"] == float64(3) {
			result := message["result"].(map[string]any)
			if result["stopReason"] != "end_turn" {
				t.Fatalf("prompt result = %+v", result)
			}
			break
		}
	}
	if !sawText {
		t.Fatal("missing agent_message_chunk")
	}
	h.close(t)
}

func TestPermissionRoundTrip(t *testing.T) {
	h := newACPHarness(t, false)
	h.mock.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "bash", Arguments: `{"command":"echo tool-ok"}`,
	}}})
	h.mock.AddText("finished")

	h.send(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	h.readUntil(t, func(m map[string]any) bool { return m["id"] == float64(1) })
	h.send(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{"cwd": t.TempDir()}})
	newSession := h.readUntil(t, func(m map[string]any) bool { return m["id"] == float64(2) })
	sessionID := newSession["result"].(map[string]any)["sessionId"].(string)

	h.send(t, map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "session/prompt",
		"params": map[string]any{
			"sessionId": sessionID,
			"prompt":    []any{map[string]any{"type": "text", "text": "run bash"}},
		},
	})

	permissionRequest := h.readUntil(t, func(m map[string]any) bool {
		return m["method"] == "session/request_permission"
	})
	requestID := permissionRequest["id"]
	h.send(t, map[string]any{
		"jsonrpc": "2.0", "id": requestID,
		"result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow-once"}},
	})

	sawCompleted := false
	for {
		message := h.read(t)
		if message["method"] == "session/update" {
			params := message["params"].(map[string]any)
			update := params["update"].(map[string]any)
			if update["sessionUpdate"] == "tool_call_update" && update["status"] == "completed" {
				sawCompleted = true
			}
			continue
		}
		if message["id"] == float64(3) {
			break
		}
	}
	if !sawCompleted {
		t.Fatal("missing completed tool_call_update")
	}
	h.close(t)
}

func TestUnknownMethod(t *testing.T) {
	h := newACPHarness(t, true)
	h.send(t, map[string]any{"jsonrpc": "2.0", "id": 9, "method": "nope"})
	message := h.read(t)
	errObj, ok := message["error"].(map[string]any)
	if !ok || errObj["code"] != float64(-32601) {
		t.Fatalf("message = %+v", message)
	}
	h.close(t)
}
