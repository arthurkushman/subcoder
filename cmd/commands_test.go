package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arthurkushman/subcoder/internal/session"
)

func writeCommandFile(t *testing.T, dir, name, content string) {
	t.Helper()
	target := filepath.Join(dir, ".subcoder", "command")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCommandsCommand(t *testing.T) {
	env := setupEnv(t, `{}`)
	writeCommandFile(t, env.dir, "review", "---\ndescription: Review code\n---\nReview $ARGUMENTS")

	out, _, err := execute(t, "commands", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/review") || !strings.Contains(out, "Review code") {
		t.Fatalf("out = %q", out)
	}

	out, _, err = execute(t, "commands", "--directory", env.dir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(parsed) != 1 || parsed[0].Name != "review" {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestRunCustomCommand(t *testing.T) {
	server := sseOpenAIServer(t, false)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, true))

	writeCommandFile(t, env.dir, "review", "---\ndescription: Review code\n---\nReview $ARGUMENTS against @notes.txt")
	if err := os.WriteFile(filepath.Join(env.dir, "notes.txt"), []byte("project-notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := execute(t, "run", "--command", "review", "app.go", "--directory", env.dir); err != nil {
		t.Fatal(err)
	}

	sess := latestSession(t, env)
	user := sess.Messages[0].Text()
	if !strings.Contains(user, "Review app.go against") || !strings.Contains(user, "project-notes") {
		t.Fatalf("user message = %q", user)
	}
}

func TestRunAttachesFiles(t *testing.T) {
	server := sseOpenAIServer(t, false)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, true))

	if err := os.WriteFile(filepath.Join(env.dir, "notes.txt"), []byte("attached-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := execute(t, "run", "hello", "-f", "notes.txt", "--directory", env.dir); err != nil {
		t.Fatal(err)
	}

	sess := latestSession(t, env)
	if len(sess.Messages) != 3 {
		t.Fatalf("messages = %d", len(sess.Messages))
	}
	if sess.Messages[0].Text() != "hello" {
		t.Fatalf("user message = %q", sess.Messages[0].Text())
	}
	if !sess.Messages[1].Synthetic || !strings.Contains(sess.Messages[1].Text(), "attached-content") {
		t.Fatalf("context message = %+v", sess.Messages[1])
	}
}

func TestRunUnknownCommand(t *testing.T) {
	env := setupEnv(t, `{}`)
	if _, _, err := execute(t, "run", "--command", "nope", "hi", "--directory", env.dir); err == nil {
		t.Fatal("expected error")
	}
}

func latestSession(t *testing.T, env *testEnv) *session.Session {
	t.Helper()
	store := session.NewStore(env.dataDir)
	sess, err := store.Latest(env.dir)
	if err != nil {
		t.Fatal(err)
	}
	return sess
}
