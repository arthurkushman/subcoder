package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testEnv struct {
	configPath string
	dataDir    string
	dir        string
}

func setupEnv(t *testing.T, configJSON string) *testEnv {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "project")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(root, "subcoder.json")
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SUBCODER_CONFIG", configPath)
	t.Setenv("SUBCODER_DATA_DIR", dataDir)
	t.Setenv("SUBCODER_CONFIG_DIR", filepath.Join(root, "global"))

	return &testEnv{configPath: configPath, dataDir: dataDir, dir: dir}
}

func execute(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	globals = globalOptions{}

	root := NewRootCommand()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func sseOpenAIServer(t *testing.T, withToolCall bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}

		var body struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sawTool := false
		for _, message := range body.Messages {
			if message.Role == "tool" {
				sawTool = true
			}
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)

		if withToolCall && !sawTool {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"echo hi\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"final answer\"}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":4}}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

func openAIConfig(serverURL string, autoApprove bool) string {
	return fmt.Sprintf(`{
		"model": "openai/gpt-test",
		"autoApprove": %v,
		"provider": {
			"openai": {
				"baseURL": %q,
				"apiKey": "test-key",
				"models": {"gpt-test": {"name": "Test Model"}}
			}
		}
	}`, autoApprove, serverURL)
}

func TestRunCommandTextFormat(t *testing.T) {
	server := sseOpenAIServer(t, false)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, true))

	out, _, err := execute(t, "run", "hello", "--directory", env.dir)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "final answer") {
		t.Fatalf("output = %q", out)
	}
}

func TestRunCommandJSONFormat(t *testing.T) {
	server := sseOpenAIServer(t, false)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, true))

	out, _, err := execute(t, "run", "hello", "--directory", env.dir, "--format", "json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var types []string
	var result map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid json line %q: %v", line, err)
		}
		types = append(types, fmt.Sprint(event["type"]))
		if event["type"] == "result" {
			result = event
		}
	}

	joined := strings.Join(types, ",")
	for _, want := range []string{"message.updated", "session.idle", "result"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
	if result["text"] != "final answer" {
		t.Fatalf("result = %+v", result)
	}
	if result["sessionID"] == "" {
		t.Fatalf("missing session id: %+v", result)
	}
}

func TestRunCommandPermissionDeniedHeadless(t *testing.T) {
	server := sseOpenAIServer(t, true)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, false))

	out, errOut, err := execute(t, "run", "run bash", "--directory", env.dir)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "final answer") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(errOut, "permission denied") {
		t.Fatalf("stderr = %q", errOut)
	}
}

func TestRunAutoApproveExecutesTool(t *testing.T) {
	server := sseOpenAIServer(t, true)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, true))

	out, errOut, err := execute(t, "run", "run bash", "--directory", env.dir)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "final answer") {
		t.Fatalf("output = %q", out)
	}
	if !strings.Contains(errOut, "echo hi") {
		t.Fatalf("tool call not surfaced: %q", errOut)
	}
}

func TestRunContinueSession(t *testing.T) {
	server := sseOpenAIServer(t, false)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, true))

	if _, _, err := execute(t, "run", "first", "--directory", env.dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "run", "second", "--directory", env.dir, "--continue"); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "session", "list", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "sessions") > 0 {
		t.Fatal("unexpected output")
	}
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("expected a single session, got:\n%s", out)
	}
}

func TestAuthCommands(t *testing.T) {
	env := setupEnv(t, `{"model":"openai/x"}`)

	out, _, err := execute(t, "auth", "login", "--provider", "anthropic", "--key", "sk-ant-secret123", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "anthropic") {
		t.Fatalf("out = %q", out)
	}

	out, _, err = execute(t, "auth", "list", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "anthropic") || !strings.Contains(out, "sk-a") {
		t.Fatalf("list = %q", out)
	}
	if strings.Contains(out, "secret123") {
		t.Fatalf("key not masked: %q", out)
	}

	out, _, err = execute(t, "auth", "logout", "--provider", "anthropic", "--directory", env.dir)
	if err != nil || !strings.Contains(out, "Removed") {
		t.Fatalf("logout out=%q err=%v", out, err)
	}

	out, _, err = execute(t, "auth", "list", "--directory", env.dir)
	if err != nil || !strings.Contains(out, "No credentials") {
		t.Fatalf("list after logout = %q", out)
	}
}

func TestModelsCommandUsesConfiguredModels(t *testing.T) {
	env := setupEnv(t, openAIConfig("http://127.0.0.1:1", true))

	out, _, err := execute(t, "models", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "openai/gpt-test") {
		t.Fatalf("out = %q", out)
	}
}

func TestInitCommand(t *testing.T) {
	env := setupEnv(t, `{}`)
	if err := os.WriteFile(filepath.Join(env.dir, "go.mod"), []byte("module x"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "init", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "AGENTS.md") {
		t.Fatalf("out = %q", out)
	}

	data, err := os.ReadFile(filepath.Join(env.dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "go test ./...") {
		t.Fatalf("content = %q", data)
	}

	if _, _, err := execute(t, "init", "--directory", env.dir); err == nil {
		t.Fatal("expected error when AGENTS.md exists")
	}
}

func TestSessionCommands(t *testing.T) {
	env := setupEnv(t, `{}`)

	out, _, err := execute(t, "session", "new", "--directory", env.dir, "--title", "my session")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out)
	if !strings.HasPrefix(id, "ses_") {
		t.Fatalf("id = %q", id)
	}

	out, _, err = execute(t, "session", "list", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id) || !strings.Contains(out, "my session") {
		t.Fatalf("list = %q", out)
	}

	out, _, err = execute(t, "session", "show", id, "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, id) {
		t.Fatalf("show = %q", out)
	}

	out, _, err = execute(t, "session", "delete", id, "--directory", env.dir)
	if err != nil || !strings.Contains(out, "Deleted") {
		t.Fatalf("delete out=%q err=%v", out, err)
	}
}

func TestExportImportCommands(t *testing.T) {
	env := setupEnv(t, `{}`)

	out, _, err := execute(t, "session", "new", "--directory", env.dir, "--title", "export me")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(out)

	exportPath := filepath.Join(env.dir, "session.json")
	if _, _, err := execute(t, "export", id, "--directory", env.dir, "-o", exportPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exportPath); err != nil {
		t.Fatal(err)
	}

	if _, _, err := execute(t, "session", "delete", id, "--directory", env.dir); err != nil {
		t.Fatal(err)
	}
	out, _, err = execute(t, "import", exportPath, "--directory", env.dir)
	if err != nil || !strings.Contains(out, id) {
		t.Fatalf("import out=%q err=%v", out, err)
	}

	out, _, err = execute(t, "session", "show", id, "--directory", env.dir)
	if err != nil || !strings.Contains(out, "export me") {
		t.Fatalf("show after import = %q", out)
	}
}

func TestStatsCommand(t *testing.T) {
	server := sseOpenAIServer(t, false)
	defer server.Close()
	env := setupEnv(t, openAIConfig(server.URL, true))

	if _, _, err := execute(t, "run", "hello", "--directory", env.dir); err != nil {
		t.Fatal(err)
	}

	out, _, err := execute(t, "stats", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Sessions: 1") {
		t.Fatalf("stats = %q", out)
	}
	if !strings.Contains(out, "input=12 output=4") {
		t.Fatalf("stats = %q", out)
	}
}

func TestAgentCreateAndList(t *testing.T) {
	env := setupEnv(t, `{}`)

	out, _, err := execute(t, "agent", "create",
		"--directory", env.dir,
		"--name", "reviewer",
		"--description", "Reviews code",
		"--prompt", "You review code carefully.",
		"--model", "openai/gpt-test",
		"--tools", "read,grep",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reviewer") {
		t.Fatalf("create out = %q", out)
	}

	path := filepath.Join(env.dir, ".subcoder", "agent", "reviewer.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"description: Reviews code", "tools: [read,grep]", "You review code carefully."} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("agent file missing %q:\n%s", want, data)
		}
	}

	out, _, err = execute(t, "agent", "list", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reviewer") || !strings.Contains(out, "build") {
		t.Fatalf("list = %q", out)
	}
}

func TestMCPListAddRemove(t *testing.T) {
	env := setupEnv(t, `{"mcp":{"existing":{"type":"local","command":["echo"]}}}`)

	out, _, err := execute(t, "mcp", "list", "--directory", env.dir)
	if err != nil || !strings.Contains(out, "existing") {
		t.Fatalf("list out=%q err=%v", out, err)
	}

	out, _, err = execute(t, "mcp", "add", "--directory", env.dir,
		"--name", "fetcher", "--command", "npx", "--command", "-y", "--command", "mcp-fetch")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fetcher") {
		t.Fatalf("add out = %q", out)
	}

	data, err := os.ReadFile(filepath.Join(env.dir, "subcoder.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "fetcher") {
		t.Fatalf("config = %q", data)
	}

	out, _, err = execute(t, "mcp", "remove", "fetcher", "--directory", env.dir)
	if err != nil || !strings.Contains(out, "Removed") {
		t.Fatalf("remove out=%q err=%v", out, err)
	}

	out, _, err = execute(t, "mcp", "list", "--directory", env.dir)
	if err != nil || strings.Contains(out, "fetcher") {
		t.Fatalf("list after remove = %q", out)
	}
}

func TestVersionAndHelp(t *testing.T) {
	env := setupEnv(t, `{}`)

	out, _, err := execute(t, "version", "--directory", env.dir)
	if err != nil || !strings.Contains(out, "0.1.0") {
		t.Fatalf("version out=%q err=%v", out, err)
	}

	out, _, err = execute(t, "--help", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"run", "serve", "auth", "models", "agent", "session", "mcp", "stats", "init", "export", "import"} {
		if !strings.Contains(out, command) {
			t.Fatalf("help missing %q:\n%s", command, out)
		}
	}
}
