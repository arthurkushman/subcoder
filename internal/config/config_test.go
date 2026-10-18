package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStripJSONC(t *testing.T) {
	in := []byte(`{
	// line comment
	"model": "openai/gpt", /* block
	comment */
	"provider": {
		"openai": {"baseURL": "https://x", "apiKey": "http://not-a-comment"},
	},
	"instructions": [
		"one", // trailing
		"two",
	],
}`)

	cfg, err := Parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Model != "openai/gpt" {
		t.Fatalf("model = %q", cfg.Model)
	}
	if cfg.Providers["openai"].APIKey != "http://not-a-comment" {
		t.Fatalf("apiKey mangled: %q", cfg.Providers["openai"].APIKey)
	}
	if len(cfg.Instructions) != 2 {
		t.Fatalf("instructions = %v", cfg.Instructions)
	}
}

func TestLoadMergeProjectOverGlobal(t *testing.T) {
	dir := t.TempDir()
	globalDir := filepath.Join(dir, "global")
	projectDir := filepath.Join(dir, "project")

	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}

	global := `{"model":"openai/global","theme":"dark","provider":{"openai":{"baseURL":"https://global"}}}`
	if err := os.WriteFile(filepath.Join(globalDir, "subcoder.json"), []byte(global), 0o644); err != nil {
		t.Fatal(err)
	}
	project := `{"model":"anthropic/project","provider":{"anthropic":{"apiKey":"k"}}}`
	if err := os.WriteFile(filepath.Join(projectDir, "subcoder.jsonc"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SUBCODER_CONFIG", filepath.Join(globalDir, "subcoder.json"))
	cfg, err := Load(projectDir)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Model != "anthropic/project" {
		t.Fatalf("model = %q", cfg.Model)
	}
	if cfg.Theme != "dark" {
		t.Fatalf("theme = %q", cfg.Theme)
	}
	if cfg.Providers["openai"].BaseURL != "https://global" {
		t.Fatalf("openai baseURL = %q", cfg.Providers["openai"].BaseURL)
	}
	if cfg.Providers["anthropic"].APIKey != "k" {
		t.Fatalf("anthropic key = %q", cfg.Providers["anthropic"].APIKey)
	}
	if cfg.ProjectPath != filepath.Join(projectDir, "subcoder.jsonc") {
		t.Fatalf("project path = %q", cfg.ProjectPath)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subcoder.json")

	cfg := New()
	cfg.Model = "mock/test"
	cfg.Instructions = []string{"be nice"}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	loaded, err := Parse(mustRead(t, path))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Model != "mock/test" || len(loaded.Instructions) != 1 {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}

	var raw map[string]any
	if err := json.Unmarshal(mustRead(t, path), &raw); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFindProjectConfigWalksUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "subcoder.json")
	if err := os.WriteFile(path, []byte(`{"model":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectConfig(nested); got != path {
		t.Fatalf("got %q want %q", got, path)
	}
}

func TestEnvKey(t *testing.T) {
	if got := EnvKey("openrouter"); got != "SUBCODER_OPENROUTER_API_KEY" {
		t.Fatalf("got %q", got)
	}
}

func TestSnapshotsEnabledByDefault(t *testing.T) {
	cfg := New()
	if !cfg.SnapshotsEnabled() {
		t.Fatal("snapshots should default to enabled")
	}
	disabled := false
	cfg.Snapshots = &disabled
	if cfg.SnapshotsEnabled() {
		t.Fatal("snapshots should be disabled")
	}
}

func TestMergeHooksAppends(t *testing.T) {
	base := New()
	base.Hooks = map[string][]HookConfig{"tool.before": {{Command: "global"}}}
	override := New()
	override.Hooks = map[string][]HookConfig{"tool.before": {{Command: "project"}}}
	base.merge(override)

	hooks := base.Hooks["tool.before"]
	if len(hooks) != 2 || hooks[0].Command != "global" || hooks[1].Command != "project" {
		t.Fatalf("hooks = %+v", hooks)
	}
}
