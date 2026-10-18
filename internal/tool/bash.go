package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	defaultBashTimeout = 120 * time.Second
	maxBashOutput      = 30_000
)

type BashTool struct{}

func NewBash() *BashTool { return &BashTool{} }

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	return "Execute a shell command in the working directory and return its combined output. Use for build, test, git and other terminal commands."
}

func (t *BashTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"command": stringProp("Shell command to execute"),
		"timeout": intProp("Timeout in seconds (default 120)"),
		"workdir": stringProp("Working directory (defaults to the project directory)"),
	}, "command")
}

type bashArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
	Workdir string `json:"workdir"`
}

func (t *BashTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args bashArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("bash: invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Command) == "" {
		return Result{}, fmt.Errorf("bash: command is required")
	}

	timeout := defaultBashTimeout
	if args.Timeout > 0 {
		timeout = time.Duration(args.Timeout) * time.Second
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(runCtx, "cmd", "/c", args.Command)
	} else {
		cmd = exec.CommandContext(runCtx, "sh", "-c", args.Command)
	}
	cmd.Dir = resolvePath(env, args.Workdir)

	output, err := cmd.CombinedOutput()
	if runCtx.Err() == context.DeadlineExceeded {
		return Result{}, fmt.Errorf("bash: timed out after %s", timeout)
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return Result{}, fmt.Errorf("bash: %w", err)
		}
	}

	text := string(output)
	truncated := false
	if len(text) > maxBashOutput {
		text = text[:maxBashOutput]
		truncated = true
	}
	if text == "" && exitCode == 0 {
		text = "(no output)"
	}
	if truncated {
		text += "\n... output truncated"
	}

	title := args.Command
	if len(title) > 60 {
		title = title[:60] + "..."
	}

	return Result{
		Title:  fmt.Sprintf("$ %s", title),
		Output: text,
		Metadata: map[string]any{
			"exitCode":  exitCode,
			"truncated": truncated,
		},
	}, nil
}
