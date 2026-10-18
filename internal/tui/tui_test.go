package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arthurkushman/subcoder/internal/agent"
	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/provider/mock"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/arthurkushman/subcoder/internal/tool"
	tea "github.com/charmbracelet/bubbletea"
)

type tuiHarness struct {
	model    *model
	provider *mock.Provider
	engine   *permission.Engine
	store    *session.Store
	dir      string
}

func newTUIHarness(t *testing.T, autoApprove bool) *tuiHarness {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("SUBCODER_CONFIG_DIR", t.TempDir())
	store := session.NewStore(t.TempDir())
	mockProvider := mock.New("mock")
	providers := provider.NewRegistry()
	providers.Register(mockProvider)
	providers.SetDefault("mock/mock-small")

	engine := permission.New(nil, nil, autoApprove)
	runner := agent.NewRunner(providers, nil, store, engine)
	runner.Agents = agent.Builtins()
	runner.DefaultModel = "mock/mock-small"

	tools := tool.NewRegistry(tool.NewRead(), tool.NewWrite(), tool.NewBash(), tool.NewTask(runner))
	runner.Tools = tools

	m := NewModel(Options{
		Config:     config.New(),
		Providers:  providers,
		Sessions:   store,
		Runner:     runner,
		Agents:     agent.Builtins(),
		Permission: engine,
		Directory:  dir,
		Model:      "mock/mock-small",
	})
	m.Init()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})

	return &tuiHarness{model: m, provider: mockProvider, engine: engine, store: store, dir: dir}
}

func (h *tuiHarness) typeKey(t *testing.T, value string) {
	t.Helper()
	for _, r := range value {
		h.model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func (h *tuiHarness) submit(t *testing.T, text string) {
	t.Helper()
	h.typeKey(t, text)
	h.model.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func (h *tuiHarness) pump(t *testing.T) {
	t.Helper()
	deadline := time.After(10 * time.Second)

	for {
		if !h.model.streaming {
			// drain queued events briefly
			select {
			case msg := <-h.model.events:
				h.model.Update(msg)
				continue
			case <-time.After(50 * time.Millisecond):
				return
			}
		}
		select {
		case msg := <-h.model.events:
			h.model.Update(msg)
		case <-deadline:
			t.Fatal("timed out waiting for agent events")
		}
	}
}

func TestParseCommand(t *testing.T) {
	cmd, ok := parseCommand(" /models extra  ")
	if !ok || cmd.Name != "models" || len(cmd.Args) != 1 || cmd.Args[0] != "extra" {
		t.Fatalf("cmd = %+v ok=%v", cmd, ok)
	}
	if _, ok := parseCommand("hello"); ok {
		t.Fatal("plain text should not parse as command")
	}
	if _, ok := parseCommand("/"); ok {
		t.Fatal("bare slash should not parse")
	}
}

func TestHelpCommand(t *testing.T) {
	h := newTUIHarness(t, true)
	h.submit(t, "/help")

	found := false
	for _, notice := range h.model.notices {
		if strings.Contains(notice, "Commands:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notices = %v", h.model.notices)
	}
}

func TestRunMessageStreamsIntoTranscript(t *testing.T) {
	h := newTUIHarness(t, true)
	h.provider.AddText("hello from mock")

	h.submit(t, "hi there")
	h.pump(t)

	if len(h.model.messages) != 2 {
		t.Fatalf("messages = %d", len(h.model.messages))
	}
	if h.model.messages[1].Text() != "hello from mock" {
		t.Fatalf("assistant text = %q", h.model.messages[1].Text())
	}
	if h.model.title != "hi there" {
		t.Fatalf("title = %q", h.model.title)
	}
	if h.model.streaming {
		t.Fatal("still streaming")
	}

	if !strings.Contains(h.model.View(), "SubCoder") {
		t.Fatal("view missing header")
	}
}

func TestPermissionModalFlow(t *testing.T) {
	h := newTUIHarness(t, false)
	permMessages := make(chan tea.Msg, 1)
	h.engine.SetAsker(&Asker{Send: func(msg tea.Msg) {
		permMessages <- msg
	}})

	h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "bash", Arguments: `{"command":"echo hi"}`,
	}}})
	h.provider.AddText("finished")

	h.submit(t, "run bash")

	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-permMessages:
			h.model.Update(msg)
			if h.model.permReq == nil {
				t.Fatal("permission modal not shown")
			}
			h.model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
		case msg := <-h.model.events:
			h.model.Update(msg)
		case <-deadline:
			t.Fatal("timed out")
		}
		if !h.model.streaming {
			break
		}
	}

	if len(h.model.messages) != 3 {
		t.Fatalf("messages = %d", len(h.model.messages))
	}
	call := h.model.messages[1].ToolCalls()[0]
	if call.State != session.ToolCompleted {
		t.Fatalf("tool state = %v (%s)", call.State, call.Error)
	}
	if !strings.Contains(call.Output, "hi") {
		t.Fatalf("tool output = %q", call.Output)
	}
}

func TestPermissionModalReject(t *testing.T) {
	h := newTUIHarness(t, false)
	permMessages := make(chan tea.Msg, 1)
	h.engine.SetAsker(&Asker{Send: func(msg tea.Msg) {
		permMessages <- msg
	}})

	h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "bash", Arguments: `{"command":"rm -rf /"}`,
	}}})
	h.provider.AddText("ok")

	h.submit(t, "delete everything")

	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-permMessages:
			h.model.Update(msg)
			h.model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
		case msg := <-h.model.events:
			h.model.Update(msg)
		case <-deadline:
			t.Fatal("timed out")
		}
		if !h.model.streaming {
			break
		}
	}

	call := h.model.messages[1].ToolCalls()[0]
	if call.State != session.ToolError || !strings.Contains(call.Error, "denied") {
		t.Fatalf("call = %+v", call)
	}
}

func TestAgentCycleKey(t *testing.T) {
	h := newTUIHarness(t, true)
	before := h.model.agentName
	h.model.Update(tea.KeyMsg{Type: tea.KeyTab})
	if h.model.agentName == before {
		t.Fatalf("agent did not cycle from %q", before)
	}
}

func TestPickerSelectModel(t *testing.T) {
	h := newTUIHarness(t, true)
	h.model.openPicker(pickerModels, "Models", []pickerItem{
		{id: "mock/mock-small", label: "mock/mock-small"},
		{id: "mock/mock-large", label: "mock/mock-large"},
	})

	h.model.Update(tea.KeyMsg{Type: tea.KeyDown})
	h.model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if h.model.modelID != "mock/mock-large" {
		t.Fatalf("model = %q", h.model.modelID)
	}
	if h.model.picker != nil {
		t.Fatal("picker should be closed")
	}
}

func TestUndoRedo(t *testing.T) {
	h := newTUIHarness(t, true)
	h.provider.AddText("answer one")
	h.submit(t, "question one")
	h.pump(t)

	if len(h.model.session.Messages) != 2 {
		t.Fatalf("messages = %d", len(h.model.session.Messages))
	}

	h.submit(t, "/undo")
	if len(h.model.session.Messages) != 0 {
		t.Fatalf("after undo messages = %d", len(h.model.session.Messages))
	}

	h.submit(t, "/redo")
	if len(h.model.session.Messages) != 2 {
		t.Fatalf("after redo messages = %d", len(h.model.session.Messages))
	}
}

func TestShellEscape(t *testing.T) {
	h := newTUIHarness(t, true)
	cmd := h.model.submit("!echo shell-works")
	if cmd == nil {
		t.Fatal("expected shell command")
	}
	msg := cmd()
	h.model.Update(msg)

	found := false
	for _, notice := range h.model.notices {
		if strings.Contains(notice, "shell-works") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notices = %v", h.model.notices)
	}
}

func TestCompactCommand(t *testing.T) {
	h := newTUIHarness(t, true)
	h.provider.AddText("answer")
	h.submit(t, "question")
	h.pump(t)

	h.provider.AddText("THE SUMMARY")
	cmd := h.model.submit("/compact")
	if cmd == nil {
		t.Fatal("expected compact command")
	}
	h.model.Update(cmd())

	if len(h.model.session.Messages) != 2 {
		t.Fatalf("messages = %d", len(h.model.session.Messages))
	}
	if !h.model.session.Messages[0].Summary {
		t.Fatalf("first message not summary: %+v", h.model.session.Messages[0])
	}
}

func TestNewCommandResetsSession(t *testing.T) {
	h := newTUIHarness(t, true)
	h.provider.AddText("a")
	h.submit(t, "q")
	h.pump(t)

	oldID := h.model.session.ID
	h.submit(t, "/new")
	if h.model.session.ID == oldID {
		t.Fatal("session not reset")
	}
	if len(h.model.messages) != 0 {
		t.Fatalf("messages = %d", len(h.model.messages))
	}
}

func TestInitCommandWritesAgentsMD(t *testing.T) {
	h := newTUIHarness(t, true)
	h.submit(t, "/init")

	data, err := readFile(h.dir + "/AGENTS.md")
	if err != nil {
		t.Fatalf("AGENTS.md not created: %v", err)
	}
	if !strings.Contains(data, "Project overview") {
		t.Fatalf("content = %q", data)
	}
}

func TestEventApplyIgnoresOtherSessions(t *testing.T) {
	h := newTUIHarness(t, true)
	h.model.applyEvent(session.Event{
		Type:      session.EventMessageUpdated,
		SessionID: "ses_other",
		Message:   &session.Message{ID: "m1", Role: "assistant"},
	})
	if len(h.model.messages) != 0 {
		t.Fatalf("messages = %d", len(h.model.messages))
	}
}

func TestViewRendersToolCalls(t *testing.T) {
	h := newTUIHarness(t, true)
	h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "bash", Arguments: `{"command":"echo rendered"}`,
	}}})
	h.provider.AddText("done")

	h.submit(t, "run")
	h.pump(t)

	view := h.model.View()
	if !strings.Contains(view, "done") {
		t.Fatalf("view missing assistant text:\n%s", view)
	}
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}
