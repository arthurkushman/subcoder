package snapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	work := t.TempDir()
	m, err := New(work, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return m, work
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCreateAndRestore(t *testing.T) {
	m, dir := newTestManager(t)
	ctx := context.Background()

	writeFile(t, dir, "app.go", "package main // original")
	writeFile(t, dir, "keep.txt", "keep")

	snap, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Exists(ctx, snap.ID) {
		t.Fatal("snapshot does not exist")
	}

	writeFile(t, dir, "app.go", "package main // changed")
	writeFile(t, dir, "new.txt", "created later")
	if err := os.Remove(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatal(err)
	}

	if err := m.Restore(ctx, snap.ID); err != nil {
		t.Fatal(err)
	}

	app, err := os.ReadFile(filepath.Join(dir, "app.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(app) != "package main // original" {
		t.Fatalf("app.go = %q", app)
	}
	keep, err := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if err != nil {
		t.Fatalf("keep.txt not restored: %v", err)
	}
	if string(keep) != "keep" {
		t.Fatalf("keep.txt = %q", keep)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt should have been removed, err = %v", err)
	}
}

func TestRestoreUnknownSnapshot(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.Restore(context.Background(), "deadbeef"); err == nil {
		t.Fatal("expected error")
	}
}

func TestShortID(t *testing.T) {
	if got := ShortID("0123456789abcdef"); got != "0123456" {
		t.Fatalf("got %q", got)
	}
	if got := ShortID("abc"); got != "abc" {
		t.Fatalf("got %q", got)
	}
}
