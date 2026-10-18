package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEnv(t *testing.T) *Context {
	t.Helper()
	dir := t.TempDir()
	return &Context{Directory: dir, SessionID: "ses_test", Agent: "build"}
}

func execTool(t *testing.T, tool Tool, env *Context, args map[string]any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), raw, env)
	if err != nil {
		t.Fatalf("%s: %v", tool.Name(), err)
	}
	return result
}

func TestWriteReadEdit(t *testing.T) {
	env := testEnv(t)

	write := execTool(t, NewWrite(), env, map[string]any{
		"filePath": "sub/dir/main.go",
		"content":  "package main\n\nfunc main() {}\n",
	})
	if write.Metadata["bytes"] == 0 {
		t.Fatal("expected bytes metadata")
	}

	read := execTool(t, NewRead(), env, map[string]any{"filePath": "sub/dir/main.go"})
	if !strings.Contains(read.Output, "00001| package main") {
		t.Fatalf("read output = %q", read.Output)
	}
	if !strings.Contains(read.Output, "00003| func main() {}") {
		t.Fatalf("read output = %q", read.Output)
	}

	readOffset := execTool(t, NewRead(), env, map[string]any{"filePath": "sub/dir/main.go", "offset": 2, "limit": 1})
	if readOffset.Output != "00002| " {
		t.Fatalf("offset read = %q", readOffset.Output)
	}

	edit := execTool(t, NewEdit(), env, map[string]any{
		"filePath":  "sub/dir/main.go",
		"oldString": "func main() {}",
		"newString": "func main() { println(\"hi\") }",
	})
	if edit.Metadata["replacements"] != 1 {
		t.Fatalf("edit metadata = %+v", edit.Metadata)
	}

	content, err := os.ReadFile(filepath.Join(env.Directory, "sub/dir/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `println("hi")`) {
		t.Fatalf("content = %q", content)
	}
}

func TestEditErrors(t *testing.T) {
	env := testEnv(t)
	execTool(t, NewWrite(), env, map[string]any{"filePath": "a.txt", "content": "one two one"})

	raw, _ := json.Marshal(map[string]any{"filePath": "a.txt", "oldString": "zzz", "newString": "x"})
	if _, err := NewEdit().Execute(context.Background(), raw, env); err == nil {
		t.Fatal("expected not found error")
	}

	raw, _ = json.Marshal(map[string]any{"filePath": "a.txt", "oldString": "one", "newString": "1"})
	if _, err := NewEdit().Execute(context.Background(), raw, env); err == nil {
		t.Fatal("expected non-unique error")
	}

	result := execTool(t, NewEdit(), env, map[string]any{"filePath": "a.txt", "oldString": "one", "newString": "1", "replaceAll": true})
	if result.Metadata["replacements"] != 2 {
		t.Fatalf("replace all metadata = %+v", result.Metadata)
	}
}

func TestReadMissingFile(t *testing.T) {
	env := testEnv(t)
	raw, _ := json.Marshal(map[string]any{"filePath": "missing.txt"})
	if _, err := NewRead().Execute(context.Background(), raw, env); err == nil {
		t.Fatal("expected error")
	}
}

func TestLs(t *testing.T) {
	env := testEnv(t)
	os.MkdirAll(filepath.Join(env.Directory, "dir"), 0o755)
	os.WriteFile(filepath.Join(env.Directory, "file.txt"), []byte("hello"), 0o644)

	result := execTool(t, NewLs(), env, map[string]any{})
	if !strings.HasPrefix(result.Output, "dir/") {
		t.Fatalf("output = %q", result.Output)
	}
	if !strings.Contains(result.Output, "file.txt (5 bytes)") {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestGlob(t *testing.T) {
	env := testEnv(t)
	os.MkdirAll(filepath.Join(env.Directory, "src/deep"), 0o755)
	os.WriteFile(filepath.Join(env.Directory, "src/main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(env.Directory, "src/deep/util.go"), []byte("package deep"), 0o644)
	os.WriteFile(filepath.Join(env.Directory, "README.md"), []byte("readme"), 0o644)

	result := execTool(t, NewGlob(), env, map[string]any{"pattern": "**/*.go"})
	if !strings.Contains(result.Output, "src/main.go") || !strings.Contains(result.Output, "src/deep/util.go") {
		t.Fatalf("glob output = %q", result.Output)
	}
	if strings.Contains(result.Output, "README.md") {
		t.Fatalf("glob included non-go file: %q", result.Output)
	}

	result = execTool(t, NewGlob(), env, map[string]any{"pattern": "*.md"})
	if !strings.Contains(result.Output, "README.md") {
		t.Fatalf("glob md output = %q", result.Output)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"**/*.go", "a/b/c.go", true},
		{"**/*.go", "c.go", true},
		{"*.go", "c.go", true},
		{"*.go", "a/c.go", false},
		{"src/**/*.ts", "src/a/b/x.ts", true},
		{"src/**/*.ts", "lib/x.ts", false},
		{"a/**", "a/b/c", true},
		{"**", "anything/at/all", true},
	}
	for _, tc := range cases {
		if got := matchGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestGrep(t *testing.T) {
	env := testEnv(t)
	os.MkdirAll(filepath.Join(env.Directory, "pkg"), 0o755)
	os.WriteFile(filepath.Join(env.Directory, "pkg/a.go"), []byte("package pkg\n\nfunc Hello() {}\n"), 0o644)
	os.WriteFile(filepath.Join(env.Directory, "pkg/b.txt"), []byte("Hello text\n"), 0o644)

	result := execTool(t, NewGrep(), env, map[string]any{"pattern": "func Hello", "include": "*.go"})
	if !strings.Contains(result.Output, "pkg/a.go:3: func Hello() {}") {
		t.Fatalf("grep output = %q", result.Output)
	}
	if strings.Contains(result.Output, "b.txt") {
		t.Fatalf("include filter failed: %q", result.Output)
	}

	result = execTool(t, NewGrep(), env, map[string]any{"pattern": "Hello"})
	if !strings.Contains(result.Output, "pkg/a.go") || !strings.Contains(result.Output, "pkg/b.txt") {
		t.Fatalf("grep all output = %q", result.Output)
	}

	result = execTool(t, NewGrep(), env, map[string]any{"pattern": "nomatch"})
	if result.Output != "No matches found" {
		t.Fatalf("no match output = %q", result.Output)
	}
}

func TestBash(t *testing.T) {
	env := testEnv(t)
	result := execTool(t, NewBash(), env, map[string]any{"command": "echo hello && pwd"})
	if !strings.Contains(result.Output, "hello") {
		t.Fatalf("output = %q", result.Output)
	}
	if !strings.Contains(result.Output, env.Directory) && !strings.Contains(result.Output, "/private"+env.Directory) {
		t.Fatalf("pwd output = %q", result.Output)
	}
	if result.Metadata["exitCode"] != 0 {
		t.Fatalf("exit code = %v", result.Metadata["exitCode"])
	}

	failing := execTool(t, NewBash(), env, map[string]any{"command": "exit 3"})
	if failing.Metadata["exitCode"] != 3 {
		t.Fatalf("exit code = %v", failing.Metadata["exitCode"])
	}

	raw, _ := json.Marshal(map[string]any{"command": "sleep 5", "timeout": 1})
	if _, err := NewBash().Execute(context.Background(), raw, env); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestWebFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><style>body{}</style></head><body><h1>Title</h1><p>Hello &amp; welcome</p><script>alert(1)</script></body></html>`)
	}))
	defer server.Close()

	env := testEnv(t)
	result := execTool(t, NewWebFetch(), env, map[string]any{"url": server.URL})
	if !strings.Contains(result.Output, "Title") || !strings.Contains(result.Output, "Hello & welcome") {
		t.Fatalf("output = %q", result.Output)
	}
	if strings.Contains(result.Output, "alert") || strings.Contains(result.Output, "body{}") {
		t.Fatalf("script/style not stripped: %q", result.Output)
	}

	raw, _ := json.Marshal(map[string]any{"url": "ftp://x"})
	if _, err := NewWebFetch().Execute(context.Background(), raw, env); err == nil {
		t.Fatal("expected scheme error")
	}
}

type memTodos struct {
	items []TodoItem
}

func (m *memTodos) SetTodos(items []TodoItem) { m.items = items }
func (m *memTodos) Todos() []TodoItem         { return m.items }

func TestTodo(t *testing.T) {
	env := testEnv(t)
	env.Todos = &memTodos{}

	result := execTool(t, NewTodo(), env, map[string]any{
		"todos": []map[string]any{
			{"content": "do it", "status": "in_progress", "priority": "high"},
			{"content": "done it", "status": "completed"},
		},
	})
	if !strings.Contains(result.Output, "[~] do it (high)") || !strings.Contains(result.Output, "[x] done it") {
		t.Fatalf("output = %q", result.Output)
	}
	if len(env.Todos.Todos()) != 2 {
		t.Fatalf("todos = %+v", env.Todos.Todos())
	}

	raw, _ := json.Marshal(map[string]any{"todos": []map[string]any{{"content": "x", "status": "pending"}}})
	noStore := &Context{Directory: env.Directory}
	if _, err := NewTodo().Execute(context.Background(), raw, noStore); err == nil {
		t.Fatal("expected error without todo store")
	}
}

type fakeRunner struct {
	req TaskRequest
}

func (f *fakeRunner) RunTask(ctx context.Context, req TaskRequest) (TaskResult, error) {
	f.req = req
	return TaskResult{SessionID: "ses_child", Text: "subagent report"}, nil
}

func TestTask(t *testing.T) {
	env := testEnv(t)
	runner := &fakeRunner{}
	result := execTool(t, NewTask(runner), env, map[string]any{
		"description":   "Find bugs",
		"prompt":        "look for bugs",
		"subagent_type": "general",
	})
	if result.Output != "subagent report" {
		t.Fatalf("output = %q", result.Output)
	}
	if runner.req.ParentSessionID != env.SessionID || runner.req.Agent != "general" {
		t.Fatalf("request = %+v", runner.req)
	}

	raw, _ := json.Marshal(map[string]any{"description": "d", "prompt": "p", "subagent_type": "general"})
	if _, err := NewTask(nil).Execute(context.Background(), raw, env); err == nil {
		t.Fatal("expected unavailable error")
	}
}

func TestRegistrySpecsAndSubset(t *testing.T) {
	registry := NewRegistry(NewRead(), NewWrite(), NewBash())
	specs := registry.Specs(nil)
	if len(specs) != 3 {
		t.Fatalf("specs = %d", len(specs))
	}
	for _, spec := range specs {
		var parsed map[string]any
		if err := json.Unmarshal(spec.Parameters, &parsed); err != nil {
			t.Fatalf("spec %s parameters invalid: %v", spec.Name, err)
		}
	}

	subset := registry.Subset([]string{"read", "nope"})
	if len(subset.Names()) != 1 {
		t.Fatalf("subset = %v", subset.Names())
	}
	if _, ok := subset.Get("read"); !ok {
		t.Fatal("subset missing read")
	}
}
