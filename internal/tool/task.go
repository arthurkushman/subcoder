package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type TodoTool struct{}

func NewTodo() *TodoTool { return &TodoTool{} }

func (t *TodoTool) Name() string { return "todowrite" }

func (t *TodoTool) Description() string {
	return "Create and update a structured todo list for the current session. Always pass the full list when updating."
}

func (t *TodoTool) Parameters() json.RawMessage {
	item := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content":  stringProp("Brief description of the task"),
			"status":   map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}},
			"priority": map[string]any{"type": "string", "enum": []string{"high", "medium", "low"}},
		},
		"required": []string{"content", "status"},
	}
	return schema(map[string]any{
		"todos": map[string]any{"type": "array", "description": "The full todo list", "items": item},
	}, "todos")
}

type todoArgs struct {
	Todos []TodoItem `json:"todos"`
}

func (t *TodoTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args todoArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("todowrite: invalid arguments: %w", err)
	}
	if env.Todos == nil {
		return Result{}, fmt.Errorf("todowrite: todo storage is not available")
	}

	env.Todos.SetTodos(args.Todos)

	var sb strings.Builder
	for _, item := range args.Todos {
		mark := "[ ]"
		switch item.Status {
		case "completed":
			mark = "[x]"
		case "in_progress":
			mark = "[~]"
		case "cancelled":
			mark = "[-]"
		}
		line := fmt.Sprintf("%s %s", mark, item.Content)
		if item.Priority != "" {
			line += fmt.Sprintf(" (%s)", item.Priority)
		}
		sb.WriteString(line + "\n")
	}

	return Result{
		Title:  "Update todos",
		Output: strings.TrimRight(sb.String(), "\n"),
		Metadata: map[string]any{
			"count": len(args.Todos),
		},
	}, nil
}

type TaskRequest struct {
	ParentSessionID string
	Directory       string
	Agent           string
	Description     string
	Prompt          string
}

type TaskResult struct {
	SessionID string
	Text      string
}

type TaskRunner interface {
	RunTask(ctx context.Context, req TaskRequest) (TaskResult, error)
}

type TaskTool struct {
	runner TaskRunner
}

func NewTask(runner TaskRunner) *TaskTool {
	return &TaskTool{runner: runner}
}

func (t *TaskTool) Name() string { return "task" }

func (t *TaskTool) Description() string {
	return "Launch a subagent to work on a complex, multi-step task autonomously. The subagent returns a single text report when done."
}

func (t *TaskTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"description":   stringProp("Short (3-5 words) description of the task"),
		"prompt":        stringProp("The task for the subagent to perform"),
		"subagent_type": stringProp("Agent to use, e.g. 'general' or 'explore'"),
	}, "description", "prompt", "subagent_type")
}

type taskArgs struct {
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type"`
}

func (t *TaskTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args taskArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("task: invalid arguments: %w", err)
	}
	if t.runner == nil {
		return Result{}, fmt.Errorf("task: subagents are not available")
	}
	if args.Prompt == "" {
		return Result{}, fmt.Errorf("task: prompt is required")
	}

	agentName := args.SubagentType
	if agentName == "" {
		agentName = "general"
	}

	result, err := t.runner.RunTask(ctx, TaskRequest{
		ParentSessionID: env.SessionID,
		Directory:       env.Directory,
		Agent:           agentName,
		Description:     args.Description,
		Prompt:          args.Prompt,
	})
	if err != nil {
		return Result{}, fmt.Errorf("task: %w", err)
	}

	return Result{
		Title:  args.Description,
		Output: result.Text,
		Metadata: map[string]any{
			"sessionID": result.SessionID,
			"agent":     agentName,
		},
	}, nil
}
