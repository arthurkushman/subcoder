package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverWalksUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, FileName), []byte("root rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, FileName), []byte("nested rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	// isolate global config
	t.Setenv("SUBCODER_CONFIG_DIR", filepath.Join(root, "global"))
	if err := os.MkdirAll(filepath.Join(root, "global"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "global", FileName), []byte("global rules"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := Discover(nested)
	if len(found) != 3 {
		t.Fatalf("found = %+v", found)
	}
	if found[0].Content != "nested rules" {
		t.Fatalf("first = %q", found[0].Content)
	}
	if found[2].Content != "global rules" {
		t.Fatalf("last = %q", found[2].Content)
	}

	combined := Combine(found)
	for _, want := range []string{"nested rules", "root rules", "global rules"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("combined missing %q:\n%s", want, combined)
		}
	}
}

func TestDiscoverEmpty(t *testing.T) {
	t.Setenv("SUBCODER_CONFIG_DIR", filepath.Join(t.TempDir(), "none"))
	if found := Discover(t.TempDir()); len(found) != 0 {
		t.Fatalf("found = %+v", found)
	}
}

func TestInitDetectsGo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	content := Init(dir)
	for _, want := range []string{"written in Go", "go test ./...", "main.go"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in:\n%s", want, content)
		}
	}
}
