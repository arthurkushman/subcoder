package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileContextIncludesMentionedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	context, files := FileContext("look at @main.go, then fix it", dir)
	if len(files) != 1 || files[0] != "main.go" {
		t.Fatalf("files = %v", files)
	}
	if !strings.Contains(context, `<file path="main.go">`) || !strings.Contains(context, "package main") {
		t.Fatalf("context = %q", context)
	}
}

func TestFileContextIgnoresMissingAndDuplicates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A"), 0o644); err != nil {
		t.Fatal(err)
	}

	context, files := FileContext("@a.txt @a.txt @missing.txt", dir)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	if strings.Count(context, "<file path=") != 1 {
		t.Fatalf("context = %q", context)
	}
}

func TestFileContextSkipsBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bin"), []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	context, files := FileContext("@bin", dir)
	if context != "" || len(files) != 0 {
		t.Fatalf("context = %q files = %v", context, files)
	}
}

func TestFileContextDoesNotMatchEmails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	context, files := FileContext("mail foo@bar", dir)
	if context != "" || len(files) != 0 {
		t.Fatalf("context = %q files = %v", context, files)
	}
}
