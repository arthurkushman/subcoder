package hook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arthurkushman/subcoder/internal/config"
)

func TestRunReceivesEventAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "hook.log")
	engine := New(map[string][]config.HookConfig{
		"tool.before": {{Command: `echo "$SUBCODER_TOOL" >> ` + output}},
	})
	if engine == nil {
		t.Fatal("engine is nil")
	}

	err := engine.Run(context.Background(), Event{Name: "tool.before", Tool: "bash", Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "bash" {
		t.Fatalf("log = %q", data)
	}
}

func TestFailingHookReturnsOutput(t *testing.T) {
	dir := t.TempDir()
	engine := New(map[string][]config.HookConfig{
		"tool.before": {{Command: `echo "blocked by policy"; exit 1`}},
	})

	err := engine.Run(context.Background(), Event{Name: "tool.before", Directory: dir})
	if err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("err = %v", err)
	}
}

func TestNilEngineIsNoop(t *testing.T) {
	var engine *Engine
	if engine.Has("tool.before") {
		t.Fatal("nil engine has hooks")
	}
	if err := engine.Run(context.Background(), Event{Name: "tool.before"}); err != nil {
		t.Fatal(err)
	}
}
