package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/arthurkushman/subcoder/internal/hook"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/provider/mock"
	"github.com/arthurkushman/subcoder/internal/skill"
)

func TestRunHookBlocksTool(t *testing.T) {
	h := newHarness(t, true)
	h.runner.Hooks = hook.New(map[string][]config.HookConfig{
		"tool.before": {{Command: `echo "blocked by policy"; exit 1`}},
	})
	h.provider.AddStep(mock.Step{ToolCalls: []provider.ToolCall{{
		ID: "call_1", Name: "bash", Arguments: `{"command":"echo should-not-run"}`,
	}}})
	h.provider.AddText("done")

	h.run(t, "try it")
	call := h.sess.Messages[1].ToolCalls()[0]
	if call.State != "error" {
		t.Fatalf("call state = %v", call.State)
	}
	if !strings.Contains(call.Error, "blocked by hook") || !strings.Contains(call.Error, "blocked by policy") {
		t.Fatalf("call error = %q", call.Error)
	}
	if strings.Contains(call.Output, "should-not-run") {
		t.Fatalf("tool executed: %q", call.Output)
	}
}

func TestSessionHooksFire(t *testing.T) {
	h := newHarness(t, true)
	logPath := filepath.Join(h.dir, "hooks.log")
	h.runner.Hooks = hook.New(map[string][]config.HookConfig{
		"session.start": {{Command: `echo start >> ` + logPath}},
		"session.end":   {{Command: `echo end >> ` + logPath}},
	})
	h.provider.AddText("hello")

	h.run(t, "hi")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "start\nend" {
		t.Fatalf("log = %q", data)
	}
}

func TestSystemPromptListsSkills(t *testing.T) {
	h := newHarness(t, true)
	h.runner.Skills = []skill.Skill{{Name: "release", Description: "Cut a release"}}

	prompt := h.runner.SystemPrompt(h.sess, Default(h.runner.Agents))
	if !strings.Contains(prompt, "Available skills") || !strings.Contains(prompt, "release: Cut a release") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestRunContextMessageIsSynthetic(t *testing.T) {
	h := newHarness(t, true)
	h.provider.AddText("ok")

	_, err := h.runner.Run(context.Background(), RunInput{
		Session: h.sess,
		Agent:   Default(h.runner.Agents),
		Text:    "look",
		Context: "<file path=\"a.txt\">content</file>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.sess.Messages) != 3 {
		t.Fatalf("messages = %d", len(h.sess.Messages))
	}
	contextMsg := h.sess.Messages[1]
	if !contextMsg.Synthetic || contextMsg.Role != "user" {
		t.Fatalf("context message = %+v", contextMsg)
	}
	if !strings.Contains(contextMsg.Text(), "content") {
		t.Fatalf("context text = %q", contextMsg.Text())
	}
}
