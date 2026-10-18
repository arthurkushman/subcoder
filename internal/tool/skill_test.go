package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/arthurkushman/subcoder/internal/skill"
)

func TestSkillTool(t *testing.T) {
	tool := NewSkill([]skill.Skill{{
		Name:        "release",
		Description: "Cut a release",
		Body:        "1. Tag\n2. Publish",
	}})
	env := &Context{Directory: "."}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"release"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Cut a release") || !strings.Contains(result.Output, "Publish") {
		t.Fatalf("output = %q", result.Output)
	}

	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"missing"}`), env); err == nil {
		t.Fatal("expected error for missing skill")
	}
}
