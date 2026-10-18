package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSkills(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".subcoder", "skill", "release")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: release\ndescription: Cut a release\n---\nRun the release checklist."
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	skills := Load(root)
	if len(skills) != 1 {
		t.Fatalf("skills = %+v", skills)
	}
	skill := skills[0]
	if skill.Name != "release" || skill.Description != "Cut a release" {
		t.Fatalf("skill = %+v", skill)
	}
	if !strings.Contains(skill.Body, "release checklist") {
		t.Fatalf("body = %q", skill.Body)
	}
	if _, ok := Find(skills, "release"); !ok {
		t.Fatal("find failed")
	}
}

func TestLoadFallsBackToDirectoryName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".subcoder", "skill", "lint")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("Lint the code."), 0o644); err != nil {
		t.Fatal(err)
	}

	skills := Load(root)
	if len(skills) != 1 || skills[0].Name != "lint" {
		t.Fatalf("skills = %+v", skills)
	}
	if skills[0].Description != "Lint the code." {
		t.Fatalf("description = %q", skills[0].Description)
	}
}
