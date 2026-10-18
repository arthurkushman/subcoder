package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/provider/mock"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/arthurkushman/subcoder/internal/tool"
)

type harness struct {
	runner   *Runner
	provider *mock.Provider
	store    *session.Store
	sess     *session.Session
	events   []session.Event
	dir      string
}

func newHarness(t *testing.T, autoApprove bool) *harness {
	t.Helper()

	dir := t.TempDir()
	store := session.NewStore(t.TempDir())

	mockProvider := mock.New("mock")
	providers := provider.NewRegistry()
	providers.Register(mockProvider)
	providers.SetDefault("mock/mock-small")

	runner := NewRunner(providers, nil, store, permission.New(nil, nil, autoApprove))
	runner.Agents = Builtins()
	runner.DefaultModel = "mock/mock-small"

	tools := tool.NewRegistry(
		tool.NewRead(), tool.NewWrite(), tool.NewEdit(), tool.NewBash(),
		tool.NewGlob(), tool.NewGrep(), tool.NewLs(), tool.NewTodo(),
		tool.NewTask(runner),
	)
	runner.Tools = tools

	sess := session.New(dir)
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	h := &harness{runner: runner, provider: mockProvider, store: store, sess: sess, dir: dir}
	runner.Emit = func(event session.Event) {
		h.events = append(h.events, event)
	}
	return h
}

func (h *harness) run(t *testing.T, text string) *session.Message {
	t.Helper()
	msg, err := h.runner.Run(context.Background(), RunInput{
		Session: h.sess,
		Agent:   Default(h.runner.Agents),
		Text:    text,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return msg
}

func TestRunTextOnly(t *testing.T) {
	h := newHarness(t, true)
	h.provider.AddText("hello world")

	msg := h.run(t, "hi")

	if msg.Text() != "hello world" {
		t.Fatalf("text = %q", msg.Text())
	}
	if len(h.sess.Messages) != 2 {
		t.Fatalf("messages = %d", len(h.sess.Messages))
	}
	if h.sess.Model != "mock/mock-small" {
		t.Fatalf("session model = %q", h.sess.Model)
	}

	var sawIdle, sawText bool
	for _, event := range h.events {
		if event.Type == session.EventSessionIdle {
			sawIdle = true
		}
		if event.Type == session.EventPartUpdated && event.Part != nil && event.Part.Type == session.PartText {
			sawText = true
		}
	}
	if !sawIdle || !sawText {
		t.Fatalf("events = %+v", h.events)
	}

	loaded, err := h.store.Load(h.sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("persisted messages = %d", len(loaded.Messages))
	}
}

func TestRunToolLoop(t *testing.T) {
	h := newHarness(t, true)
	h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "bash", Arguments: `{"command":"echo tool-ran"}`,
	}}})
	h.provider.AddText("done")

	msg := h.run(t, "run it")

	if msg.Text() != "done" {
		t.Fatalf("final text = %q", msg.Text())
	}
	if len(h.sess.Messages) != 3 {
		t.Fatalf("messages = %d", len(h.sess.Messages))
	}

	calls := h.sess.Messages[1].ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	if calls[0].State != session.ToolCompleted {
		t.Fatalf("state = %v (err=%s)", calls[0].State, calls[0].Error)
	}
	if !strings.Contains(calls[0].Output, "tool-ran") {
		t.Fatalf("output = %q", calls[0].Output)
	}

	requests := h.provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %d", len(requests))
	}
	second := requests[1].Messages
	var toolMsg *provider.ChatMessage
	for i := range second {
		if second[i].Role == "tool" {
			toolMsg = &second[i]
		}
	}
	if toolMsg == nil || !strings.Contains(toolMsg.Text, "tool-ran") || toolMsg.ToolCallID != "call_1" {
		t.Fatalf("tool message = %+v", toolMsg)
	}
}

func TestRunPermissionDenied(t *testing.T) {
	h := newHarness(t, false)
	h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "bash", Arguments: `{"command":"rm -rf /"}`,
	}}})
	h.provider.AddText("understood")

	h.run(t, "delete")

	call := h.sess.Messages[1].ToolCalls()[0]
	if call.State != session.ToolError {
		t.Fatalf("state = %v", call.State)
	}
	if !strings.Contains(call.Error, "permission denied") {
		t.Fatalf("error = %q", call.Error)
	}

	requests := h.provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests = %d", len(requests))
	}
	found := false
	for _, message := range requests[1].Messages {
		if message.Role == "tool" && strings.HasPrefix(message.Text, "Error: permission denied") {
			found = true
		}
	}
	if !found {
		t.Fatalf("denied tool result not fed back: %+v", requests[1].Messages)
	}
}

func TestRunUnknownTool(t *testing.T) {
	h := newHarness(t, true)
	h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "does_not_exist", Arguments: `{}`,
	}}})
	h.provider.AddText("recovered")

	h.run(t, "go")

	call := h.sess.Messages[1].ToolCalls()[0]
	if call.State != session.ToolError || !strings.Contains(call.Error, "unknown tool") {
		t.Fatalf("call = %+v", call)
	}
}

func TestRunMaxSteps(t *testing.T) {
	h := newHarness(t, true)
	h.runner.MaxSteps = 2
	for i := 0; i < 5; i++ {
		h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
			ID: "call_loop", Name: "bash", Arguments: `{"command":"echo x"}`,
		}}})
	}

	_, err := h.runner.Run(context.Background(), RunInput{
		Session: h.sess,
		Agent:   Default(h.runner.Agents),
		Text:    "loop",
	})
	if err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("err = %v", err)
	}
	if len(h.sess.Messages) != 3 {
		t.Fatalf("messages = %d", len(h.sess.Messages))
	}
}

func TestRunTaskSubagent(t *testing.T) {
	h := newHarness(t, true)
	h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_task", Name: "task",
		Arguments: `{"description":"explore","prompt":"find the answer","subagent_type":"general"}`,
	}}})
	h.provider.AddText("child report")
	h.provider.AddText("parent done")

	msg := h.run(t, "delegate")

	if msg.Text() != "parent done" {
		t.Fatalf("text = %q", msg.Text())
	}

	call := h.sess.Messages[1].ToolCalls()[0]
	if call.State != session.ToolCompleted {
		t.Fatalf("call state = %v err=%s", call.State, call.Error)
	}
	if call.Output != "child report" {
		t.Fatalf("call output = %q", call.Output)
	}

	childID, _ := call.Metadata["sessionID"].(string)
	if childID == "" {
		t.Fatalf("no child session metadata: %+v", call)
	}
	child, err := h.store.Load(childID)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != h.sess.ID {
		t.Fatalf("parent = %q", child.ParentID)
	}
	if child.Agent != "general" {
		t.Fatalf("agent = %q", child.Agent)
	}
}

func TestCompact(t *testing.T) {
	h := newHarness(t, true)
	h.sess.AddUserMessage("first question")
	h.sess.AddAssistantMessage("mock/mock-small", "mock").AddPart(session.Part{Type: session.PartText, Text: "first answer"})
	h.sess.AddUserMessage("second question")
	if err := h.store.Save(h.sess); err != nil {
		t.Fatal(err)
	}

	h.provider.AddText("THIS IS THE SUMMARY")

	summary, err := h.runner.Compact(context.Background(), h.sess, "")
	if err != nil {
		t.Fatal(err)
	}

	if !summary.Summary || summary.Text() != "THIS IS THE SUMMARY" {
		t.Fatalf("summary = %+v", summary)
	}
	if len(h.sess.Messages) != 2 {
		t.Fatalf("messages = %d", len(h.sess.Messages))
	}
	if h.sess.Messages[1].Text() != "second question" {
		t.Fatalf("kept = %q", h.sess.Messages[1].Text())
	}

	requests := h.provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	if len(requests[0].Messages) != 4 {
		t.Fatalf("summarizer messages = %d", len(requests[0].Messages))
	}
}

func TestSystemPromptIncludesRulesAndTodos(t *testing.T) {
	h := newHarness(t, true)
	if err := os.WriteFile(filepath.Join(h.dir, "AGENTS.md"), []byte("Always write tests"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.runner.Instructions = []string{"be brief"}
	h.sess.SetTodos([]session.Todo{{Content: "task one", Status: "in_progress"}})

	prompt := h.runner.SystemPrompt(h.sess, Default(h.runner.Agents))
	for _, want := range []string{"Always write tests", "be brief", "task one", h.dir} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestToChatMessages(t *testing.T) {
	msg := &session.Message{Role: "assistant"}
	msg.AddPart(session.Part{Type: session.PartText, Text: "checking"})
	msg.AddPart(session.Part{Type: session.PartTool, Tool: &session.ToolCall{
		ID: "call_1", Name: "bash", Arguments: `{"command":"ls"}`,
		State: session.ToolCompleted, Output: "file1",
	}})

	wire := ToChatMessages([]session.Message{
		{Role: "user", Parts: []session.Part{{Type: session.PartText, Text: "hello"}}},
		*msg,
	})

	if len(wire) != 3 {
		t.Fatalf("wire = %+v", wire)
	}
	if wire[0].Role != "user" || wire[1].Role != "assistant" || wire[2].Role != "tool" {
		t.Fatalf("roles = %v %v %v", wire[0].Role, wire[1].Role, wire[2].Role)
	}
	if len(wire[1].ToolCalls) != 1 || wire[1].ToolCalls[0].Name != "bash" {
		t.Fatalf("tool calls = %+v", wire[1].ToolCalls)
	}
	if wire[2].Text != "file1" || wire[2].ToolCallID != "call_1" {
		t.Fatalf("tool result = %+v", wire[2])
	}
}

func TestBuiltinsAndFind(t *testing.T) {
	agents := Builtins()
	if _, ok := Find(agents, "build"); !ok {
		t.Fatal("missing build agent")
	}
	if _, ok := Find(agents, "plan"); !ok {
		t.Fatal("missing plan agent")
	}
	def := Default(agents)
	if def.Name != "build" {
		t.Fatalf("default = %q", def.Name)
	}
	plan, _ := Find(agents, "plan")
	if !plan.IsPrimary() || plan.IsSubagent() {
		t.Fatalf("plan modes: %+v", plan)
	}
}

func TestParseMarkdownAgent(t *testing.T) {
	content := `---
description: Review bot
mode: subagent
model: mock/mock-large
temperature: 0.2
tools: [read, grep]
---
You review code.`
	agent := parseMarkdownAgent("review", content)
	if agent.Description != "Review bot" || agent.Mode != "subagent" || agent.Model != "mock/mock-large" {
		t.Fatalf("agent = %+v", agent)
	}
	if agent.Temperature == nil || *agent.Temperature != 0.2 {
		t.Fatalf("temperature = %v", agent.Temperature)
	}
	if len(agent.Tools) != 2 || agent.Tools[1] != "grep" {
		t.Fatalf("tools = %v", agent.Tools)
	}
	if agent.Prompt != "You review code." {
		t.Fatalf("prompt = %q", agent.Prompt)
	}
}
