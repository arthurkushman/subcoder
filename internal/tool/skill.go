package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arthurkushman/subcoder/internal/skill"
)

// SkillTool loads the full instructions for a discovered skill.
type SkillTool struct {
	skills []skill.Skill
}

func NewSkill(skills []skill.Skill) *SkillTool {
	return &SkillTool{skills: skills}
}

func (t *SkillTool) Name() string { return "skill" }

func (t *SkillTool) Description() string {
	return "Load the full instructions for an available skill. Use this when a task matches a skill listed in the system prompt."
}

func (t *SkillTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"name": stringProp("Name of the skill to load"),
	}, "name")
}

type skillArgs struct {
	Name string `json:"name"`
}

func (t *SkillTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args skillArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("skill: invalid arguments: %w", err)
	}
	loaded, ok := skill.Find(t.skills, args.Name)
	if !ok {
		names := skill.Names(t.skills)
		if len(names) == 0 {
			return Result{}, fmt.Errorf("skill: no skills are configured")
		}
		return Result{}, fmt.Errorf("skill: unknown skill %q (available: %s)", args.Name, strings.Join(names, ", "))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Skill: %s\n\n", loaded.Name)
	if loaded.Description != "" {
		fmt.Fprintf(&sb, "%s\n\n", loaded.Description)
	}
	sb.WriteString(loaded.Body)
	return Result{
		Title:  fmt.Sprintf("Load skill %s", loaded.Name),
		Output: strings.TrimSpace(sb.String()),
		Metadata: map[string]any{
			"name": loaded.Name,
			"path": loaded.Path,
		},
	}, nil
}
