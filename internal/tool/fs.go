package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxReadLines   = 2000
	maxListEntries = 1000
	maxFileSize    = 4 << 20
)

func resolvePath(env *Context, path string) string {
	if path == "" {
		return env.Directory
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(env.Directory, path)
}

type ReadTool struct{}

func NewRead() *ReadTool { return &ReadTool{} }

func (t *ReadTool) Name() string { return "read" }

func (t *ReadTool) Description() string {
	return "Read the contents of a file. Returns numbered lines. Use offset and limit to read specific ranges of large files."
}

func (t *ReadTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"filePath": stringProp("Absolute or relative path to the file to read"),
		"offset":   intProp("Line number to start reading from (1-based)"),
		"limit":    intProp("Maximum number of lines to read (default 2000)"),
	}, "filePath")
}

type readArgs struct {
	FilePath string `json:"filePath"`
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
}

func (t *ReadTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args readArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("read: invalid arguments: %w", err)
	}
	path := resolvePath(env, args.FilePath)

	info, err := os.Stat(path)
	if err != nil {
		return Result{}, fmt.Errorf("read: %w", err)
	}
	if info.IsDir() {
		return Result{}, fmt.Errorf("read: %s is a directory, use the ls tool", args.FilePath)
	}
	if info.Size() > maxFileSize {
		return Result{}, fmt.Errorf("read: file too large (%d bytes)", info.Size())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read: %w", err)
	}

	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	offset := args.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := args.Limit
	if limit <= 0 || limit > maxReadLines {
		limit = maxReadLines
	}
	if offset > len(lines) {
		return Result{}, fmt.Errorf("read: offset %d beyond end of file (%d lines)", offset, len(lines))
	}
	end := offset - 1 + limit
	if end > len(lines) {
		end = len(lines)
	}

	var sb strings.Builder
	for i := offset - 1; i < end; i++ {
		sb.WriteString(fmt.Sprintf("%05d| %s\n", i+1, lines[i]))
	}

	return Result{
		Title:  fmt.Sprintf("Read %s", args.FilePath),
		Output: strings.TrimRight(sb.String(), "\n"),
		Metadata: map[string]any{
			"path":  path,
			"lines": len(lines),
		},
	}, nil
}

type WriteTool struct{}

func NewWrite() *WriteTool { return &WriteTool{} }

func (t *WriteTool) Name() string { return "write" }

func (t *WriteTool) Description() string {
	return "Write content to a file, creating parent directories as needed. Overwrites existing files."
}

func (t *WriteTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"filePath": stringProp("Absolute or relative path to the file to write"),
		"content":  stringProp("Full content to write to the file"),
	}, "filePath", "content")
}

type writeArgs struct {
	FilePath string `json:"filePath"`
	Content  string `json:"content"`
}

func (t *WriteTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args writeArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("write: invalid arguments: %w", err)
	}
	path := resolvePath(env, args.FilePath)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{}, fmt.Errorf("write: %w", err)
	}
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return Result{}, fmt.Errorf("write: %w", err)
	}

	lines := strings.Count(args.Content, "\n") + 1
	return Result{
		Title:  fmt.Sprintf("Write %s", args.FilePath),
		Output: fmt.Sprintf("Wrote %d bytes (%d lines) to %s", len(args.Content), lines, args.FilePath),
		Metadata: map[string]any{
			"path":  path,
			"bytes": len(args.Content),
		},
	}, nil
}

type EditTool struct{}

func NewEdit() *EditTool { return &EditTool{} }

func (t *EditTool) Name() string { return "edit" }

func (t *EditTool) Description() string {
	return "Replace an exact string in a file with a new string. The oldString must appear exactly once unless replaceAll is true."
}

func (t *EditTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"filePath":   stringProp("Absolute or relative path to the file to edit"),
		"oldString":  stringProp("Exact text to replace"),
		"newString":  stringProp("Replacement text"),
		"replaceAll": boolProp("Replace every occurrence instead of requiring a unique match"),
	}, "filePath", "oldString", "newString")
}

type editArgs struct {
	FilePath   string `json:"filePath"`
	OldString  string `json:"oldString"`
	NewString  string `json:"newString"`
	ReplaceAll bool   `json:"replaceAll"`
}

func (t *EditTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args editArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("edit: invalid arguments: %w", err)
	}
	if args.OldString == "" {
		return Result{}, fmt.Errorf("edit: oldString must not be empty")
	}
	path := resolvePath(env, args.FilePath)

	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("edit: %w", err)
	}
	content := string(data)

	count := strings.Count(content, args.OldString)
	if count == 0 {
		return Result{}, fmt.Errorf("edit: oldString not found in %s", args.FilePath)
	}
	if count > 1 && !args.ReplaceAll {
		return Result{}, fmt.Errorf("edit: oldString appears %d times in %s; provide more context or set replaceAll", count, args.FilePath)
	}

	updated := strings.Replace(content, args.OldString, args.NewString, 1)
	if args.ReplaceAll {
		updated = strings.ReplaceAll(content, args.OldString, args.NewString)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return Result{}, fmt.Errorf("edit: %w", err)
	}

	replacements := 1
	if args.ReplaceAll {
		replacements = count
	}
	return Result{
		Title:  fmt.Sprintf("Edit %s", args.FilePath),
		Output: fmt.Sprintf("Replaced %d occurrence(s) in %s", replacements, args.FilePath),
		Metadata: map[string]any{
			"path":         path,
			"replacements": replacements,
		},
	}, nil
}

type LsTool struct{}

func NewLs() *LsTool { return &LsTool{} }

func (t *LsTool) Name() string { return "ls" }

func (t *LsTool) Description() string {
	return "List files and directories at a path. Directories are shown with a trailing slash."
}

func (t *LsTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"path": stringProp("Directory to list (defaults to the working directory)"),
	})
}

type lsArgs struct {
	Path string `json:"path"`
}

func (t *LsTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args lsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("ls: invalid arguments: %w", err)
	}
	path := resolvePath(env, args.Path)

	entries, err := os.ReadDir(path)
	if err != nil {
		return Result{}, fmt.Errorf("ls: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	var sb strings.Builder
	shown := 0
	for _, entry := range entries {
		if shown >= maxListEntries {
			sb.WriteString(fmt.Sprintf("... truncated (%d more entries)\n", len(entries)-shown))
			break
		}
		if entry.IsDir() {
			sb.WriteString(entry.Name() + "/\n")
		} else {
			info, err := entry.Info()
			if err != nil {
				sb.WriteString(entry.Name() + "\n")
				continue
			}
			sb.WriteString(fmt.Sprintf("%s (%d bytes)\n", entry.Name(), info.Size()))
		}
		shown++
	}

	display := args.Path
	if display == "" {
		display = "."
	}
	return Result{
		Title:  fmt.Sprintf("List %s", display),
		Output: strings.TrimRight(sb.String(), "\n"),
		Metadata: map[string]any{
			"path":    path,
			"entries": len(entries),
		},
	}, nil
}
