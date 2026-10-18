package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndFind(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".subcoder", "command")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: Review code\nagent: plan\nmodel: mock/mock-small\n---\nReview $ARGUMENTS"
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	commands := Load(root)
	cmd, ok := Find(commands, "review")
	if !ok {
		t.Fatalf("commands = %+v", commands)
	}
	if cmd.Description != "Review code" || cmd.Agent != "plan" || cmd.Model != "mock/mock-small" {
		t.Fatalf("cmd = %+v", cmd)
	}
	if _, ok := Find(commands, "missing"); ok {
		t.Fatal("unexpected command")
	}
}

func TestExpandArgumentsShellAndFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("file-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := Command{Name: "review", Template: "Review $1 and $ARGUMENTS\n!`echo shell-output`\n@notes.txt"}
	out, err := Expand(cmd, "config code", dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Review config and config code", "shell-output", "file-content"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestExpandAppendsArguments(t *testing.T) {
	out, err := Expand(Command{Name: "do", Template: "Do the thing."}, "now", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Arguments: now") {
		t.Fatalf("output = %q", out)
	}
}

func TestExpandMissingFileFails(t *testing.T) {
	_, err := Expand(Command{Name: "review", Template: "@missing.txt"}, "", t.TempDir())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseWithoutFrontmatter(t *testing.T) {
	cmd := parse("simple", "Just do it.")
	if cmd.Template != "Just do it." {
		t.Fatalf("template = %q", cmd.Template)
	}
}
