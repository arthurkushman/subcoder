package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arthurkushman/subcoder/internal/session"
)

func TestUpgradeCheckAndDryRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name":"v9.9.9"}`))
	}))
	defer server.Close()
	t.Setenv("SUBCODER_UPGRADE_URL", server.URL)

	env := setupEnv(t, `{}`)
	out, _, err := execute(t, "upgrade", "--check", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "latest:  v9.9.9") || !strings.Contains(out, "0.1.0") {
		t.Fatalf("out = %q", out)
	}

	out, _, err = execute(t, "upgrade", "--dry-run", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "go install github.com/arthurkushman/subcoder@latest") {
		t.Fatalf("out = %q", out)
	}

	out, _, err = execute(t, "upgrade", "1.2.3", "--dry-run", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "@v1.2.3") {
		t.Fatalf("out = %q", out)
	}
}

func TestUpgradeCheckFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no releases", http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("SUBCODER_UPGRADE_URL", server.URL)

	env := setupEnv(t, `{}`)
	if _, _, err := execute(t, "upgrade", "--check", "--directory", env.dir); err == nil {
		t.Fatal("expected error")
	}
}

func TestUninstallDryRun(t *testing.T) {
	env := setupEnv(t, `{}`)
	out, _, err := execute(t, "uninstall", "--dry-run", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "would remove configuration") || !strings.Contains(out, "would remove session data") {
		t.Fatalf("out = %q", out)
	}
	if _, err := os.Stat(env.dataDir); err != nil {
		t.Fatalf("dry run removed data: %v", err)
	}
}

func TestDebugCommandsRedactSecrets(t *testing.T) {
	env := setupEnv(t, `{"provider":{"openai":{"apiKey":"secret-key","models":{"m":{"name":"M"}}}}}`)
	t.Setenv("SUBCODER_TEST_TOKEN", "top-secret")

	out, _, err := execute(t, "debug", "config", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "secret-key") || !strings.Contains(out, "***") {
		t.Fatalf("out = %q", out)
	}

	out, _, err = execute(t, "debug", "env", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "top-secret") || !strings.Contains(out, "SUBCODER_TEST_TOKEN=***") {
		t.Fatalf("env = %q", out)
	}

	out, _, err = execute(t, "debug", "paths", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, env.dataDir) || !strings.Contains(out, env.dir) {
		t.Fatalf("paths = %q", out)
	}
}

func TestDBPath(t *testing.T) {
	env := setupEnv(t, `{}`)
	out, _, err := execute(t, "db", "path", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != filepath.Join(env.dataDir, "sessions") {
		t.Fatalf("path = %q", out)
	}
}

func TestSessionListJSONAndLimit(t *testing.T) {
	env := setupEnv(t, `{}`)
	store := session.NewStore(env.dataDir)
	for i := 0; i < 3; i++ {
		sess := session.New(env.dir)
		sess.Title = "session"
		sess.UpdatedAt = sess.UpdatedAt.Add(time.Duration(i) * time.Minute)
		if err := store.Save(sess); err != nil {
			t.Fatal(err)
		}
	}

	out, _, err := execute(t, "session", "list", "--format", "json", "-n", "2", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	var summaries []session.Summary
	if err := json.Unmarshal([]byte(out), &summaries); err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Fatalf("summaries = %d", len(summaries))
	}
}

func TestExportSanitize(t *testing.T) {
	env := setupEnv(t, `{}`)
	store := session.NewStore(env.dataDir)
	sess := session.New(env.dir)
	sess.AddUserMessage("super-secret")
	assistant := sess.AddAssistantMessage("mock/mock-small", "mock")
	assistant.AddPart(session.Part{Type: session.PartText, Text: "secret answer"})
	if err := store.Save(sess); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "export", sess.ID, "--sanitize", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "super-secret") || strings.Contains(out, "secret answer") {
		t.Fatalf("not sanitized: %s", out)
	}
	if !strings.Contains(out, "[redacted]") {
		t.Fatalf("missing redaction: %s", out)
	}
}

func TestRunTitleAndFork(t *testing.T) {
	server := sseOpenAIServer(t, false)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, true))

	if _, _, err := execute(t, "run", "first", "--title", "Named", "--directory", env.dir); err != nil {
		t.Fatal(err)
	}
	first := latestSession(t, env)
	if first.Title != "Named" {
		t.Fatalf("title = %q", first.Title)
	}

	if _, _, err := execute(t, "run", "second", "--continue", "--fork", "--directory", env.dir); err != nil {
		t.Fatal(err)
	}
	forked := latestSession(t, env)
	if forked.ID == first.ID {
		t.Fatal("fork reused session id")
	}
	if forked.ParentID != first.ID {
		t.Fatalf("parent = %q", forked.ParentID)
	}
	if len(forked.Messages) != 4 {
		t.Fatalf("fork messages = %d", len(forked.Messages))
	}
	loaded, err := session.NewStore(env.dataDir).Load(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 2 {
		t.Fatalf("original messages = %d", len(loaded.Messages))
	}
}
