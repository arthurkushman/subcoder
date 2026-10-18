package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubInstall(t *testing.T) {
	env := setupEnv(t, `{}`)

	out, _, err := execute(t, "github", "install", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "subcoder.yml") {
		t.Fatalf("out = %q", out)
	}
	path := filepath.Join(env.dir, ".github", "workflows", "subcoder.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "subcoder github run") {
		t.Fatalf("workflow = %q", data)
	}

	if _, _, err := execute(t, "github", "install", "--directory", env.dir); err == nil {
		t.Fatal("expected error when workflow exists")
	}
	if _, _, err := execute(t, "github", "install", "--force", "--directory", env.dir); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubRun(t *testing.T) {
	server := sseOpenAIServer(t, false)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, true))

	eventPath := filepath.Join(env.dir, "event.json")
	event := `{"action":"opened","issue":{"number":7,"title":"Bug","body":"It crashes"},"repository":{"full_name":"acme/app"}}`
	if err := os.WriteFile(eventPath, []byte(event), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "github", "run", "--event", eventPath, "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "final answer") {
		t.Fatalf("out = %q", out)
	}

	sess := latestSession(t, env)
	text := sess.Messages[0].Text()
	if !strings.Contains(text, "acme/app") || !strings.Contains(text, "It crashes") {
		t.Fatalf("prompt = %q", text)
	}
}

func TestPromptFromEvent(t *testing.T) {
	prompt, err := promptFromEvent([]byte(`{"action":"opened","pull_request":{"number":2,"title":"Add feature","body":"Details"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Pull request #2") || !strings.Contains(prompt, "Details") {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestCheckoutPR(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" > gh.args\n"
	path := filepath.Join(bin, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	dir := t.TempDir()
	if err := checkoutPR(t.Context(), dir, "42"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "gh.args"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "pr checkout 42" {
		t.Fatalf("args = %q", data)
	}
}
