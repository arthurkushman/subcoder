package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arthurkushman/subcoder/internal/command"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/provider/mock"
	"github.com/arthurkushman/subcoder/internal/snapshot"
	tea "github.com/charmbracelet/bubbletea"
)

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCustomCommandExpandsAndRuns(t *testing.T) {
	h := newTUIHarness(t, true)
	writeTestFile(t, h.dir, ".subcoder/command/review.md", "---\ndescription: Review code\n---\nReview $ARGUMENTS against @notes.txt")
	writeTestFile(t, h.dir, "notes.txt", "project-notes")
	h.model.commands = command.Load(h.dir)
	h.provider.AddText("reviewed")

	h.submit(t, "/review main.go")
	h.pump(t)

	if len(h.model.messages) != 2 {
		t.Fatalf("messages = %d", len(h.model.messages))
	}
	user := h.model.messages[0].Text()
	if !strings.Contains(user, "Review main.go against") || !strings.Contains(user, "project-notes") {
		t.Fatalf("user message = %q", user)
	}
	if h.model.messages[1].Text() != "reviewed" {
		t.Fatalf("assistant = %q", h.model.messages[1].Text())
	}
}

func TestCommandsCommandListsCustom(t *testing.T) {
	h := newTUIHarness(t, true)
	writeTestFile(t, h.dir, ".subcoder/command/deploy.md", "---\ndescription: Deploy it\n---\nDeploy.")
	h.model.commands = command.Load(h.dir)

	h.submit(t, "/commands")
	found := false
	for _, notice := range h.model.notices {
		if strings.Contains(notice, "/deploy") && strings.Contains(notice, "Deploy it") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notices = %v", h.model.notices)
	}
}

func TestToggleCommands(t *testing.T) {
	h := newTUIHarness(t, true)

	if h.model.showThinking != true {
		t.Fatal("thinking should start enabled")
	}
	h.submit(t, "/thinking")
	if h.model.showThinking {
		t.Fatal("thinking not toggled off")
	}
	h.submit(t, "/details")
	if !h.model.details {
		t.Fatal("details not toggled on")
	}
	h.submit(t, "/timestamps")
	if !h.model.showTimestamps {
		t.Fatal("timestamps not toggled on")
	}
}

func TestThemePicker(t *testing.T) {
	h := newTUIHarness(t, true)
	h.submit(t, "/themes")
	if h.model.picker == nil || h.model.picker.kind != pickerThemes {
		t.Fatalf("picker = %+v", h.model.picker)
	}
	h.model.Update(tea.KeyMsg{Type: tea.KeyDown})
	h.model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if h.model.themeName != "dark" {
		t.Fatalf("theme = %q", h.model.themeName)
	}
}

func TestFileMentionBecomesContext(t *testing.T) {
	h := newTUIHarness(t, true)
	writeTestFile(t, h.dir, "notes.txt", "note-content")
	h.provider.AddText("answer")

	h.submit(t, "look at @notes.txt")
	h.pump(t)

	if len(h.model.messages) != 3 {
		t.Fatalf("messages = %d", len(h.model.messages))
	}
	contextMsg := h.model.messages[1]
	if !contextMsg.Synthetic || !strings.Contains(contextMsg.Text(), "note-content") {
		t.Fatalf("context message = %+v", contextMsg)
	}
	if strings.Contains(h.model.View(), "note-content") {
		t.Fatal("synthetic context should not be rendered in the transcript")
	}
}

func TestUndoRestoresFileChanges(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	h := newTUIHarness(t, true)
	writeTestFile(t, h.dir, "app.txt", "original")

	manager, err := snapshot.New(h.dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h.model.opts.Snapshots = manager

	h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID:        "call_1",
		Name:      "write",
		Arguments: `{"filePath":"app.txt","content":"changed"}`,
	}}})
	h.provider.AddText("written")
	h.submit(t, "change the file")
	h.pump(t)

	data, err := os.ReadFile(filepath.Join(h.dir, "app.txt"))
	if err != nil || string(data) != "changed" {
		t.Fatalf("after run file = %q err = %v", data, err)
	}

	h.submit(t, "/undo")
	data, err = os.ReadFile(filepath.Join(h.dir, "app.txt"))
	if err != nil || string(data) != "original" {
		t.Fatalf("after undo file = %q err = %v", data, err)
	}

	h.submit(t, "/redo")
	data, err = os.ReadFile(filepath.Join(h.dir, "app.txt"))
	if err != nil || string(data) != "changed" {
		t.Fatalf("after redo file = %q err = %v", data, err)
	}
}

func TestEditorHelpers(t *testing.T) {
	path, err := writePromptFile("draft")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "draft" {
		t.Fatalf("data = %q err = %v", data, err)
	}

	t.Setenv("SUBCODER_EDITOR", "nano")
	if got := editorCommand(); got != "nano" {
		t.Fatalf("editor = %q", got)
	}
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Fatalf("quote = %q", got)
	}
}

func TestSessionStartFlags(t *testing.T) {
	h := newTUIHarness(t, true)
	existing := h.model.session
	existing.Title = "existing"
	if err := h.store.Save(existing); err != nil {
		t.Fatal(err)
	}

	opts := h.model.opts

	fresh := NewModel(opts)
	fresh.Init()
	if fresh.session.ID == existing.ID {
		t.Fatal("default should start a new session")
	}

	opts.Continue = true
	continued := NewModel(opts)
	continued.Init()
	if continued.session.ID != existing.ID {
		t.Fatalf("continue session = %q", continued.session.ID)
	}

	opts.Fork = true
	forked := NewModel(opts)
	forked.Init()
	if forked.session.ID == existing.ID || forked.session.ParentID != existing.ID {
		t.Fatalf("fork = %+v", forked.session)
	}
}
