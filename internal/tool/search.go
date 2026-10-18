package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxGlobResults = 100
	maxGrepMatches = 100
	grepLineLimit  = 500
)

type GlobTool struct{}

func NewGlob() *GlobTool { return &GlobTool{} }

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return "Find files by glob pattern (supports ** for recursive matching). Results are sorted by modification time, newest first."
}

func (t *GlobTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"pattern": stringProp("Glob pattern such as '**/*.go' or 'src/**/*.ts'"),
		"path":    stringProp("Directory to search in (defaults to the working directory)"),
	}, "pattern")
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

type globMatch struct {
	path    string
	rel     string
	modTime time.Time
}

func (t *GlobTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args globArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("glob: invalid arguments: %w", err)
	}
	if args.Pattern == "" {
		return Result{}, fmt.Errorf("glob: pattern is required")
	}

	root := resolvePath(env, args.Path)
	pattern := filepath.ToSlash(args.Pattern)
	pattern = strings.TrimPrefix(pattern, "./")

	var matches []globMatch
	err := walkFiles(root, func(path string, info os.FileInfo) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if matchGlob(pattern, rel) {
			matches = append(matches, globMatch{path: path, rel: rel, modTime: info.ModTime()})
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("glob: %w", err)
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].modTime.After(matches[j].modTime)
	})

	var sb strings.Builder
	for i, m := range matches {
		if i >= maxGlobResults {
			sb.WriteString(fmt.Sprintf("... truncated (%d more matches)\n", len(matches)-maxGlobResults))
			break
		}
		sb.WriteString(m.rel + "\n")
	}
	if len(matches) == 0 {
		sb.WriteString("No files found")
	}

	return Result{
		Title:  fmt.Sprintf("Glob %s", args.Pattern),
		Output: strings.TrimRight(sb.String(), "\n"),
		Metadata: map[string]any{
			"matches": len(matches),
		},
	}, nil
}

type GrepTool struct{}

func NewGrep() *GrepTool { return &GrepTool{} }

func (t *GrepTool) Name() string { return "grep" }

func (t *GrepTool) Description() string {
	return "Search file contents with a regular expression. Returns matching lines as file:line: text."
}

func (t *GrepTool) Parameters() json.RawMessage {
	return schema(map[string]any{
		"pattern": stringProp("Regular expression to search for"),
		"include": stringProp("Optional glob filter for file names, e.g. '*.go' or '**/*.ts'"),
		"path":    stringProp("Directory to search in (defaults to the working directory)"),
	}, "pattern")
}

type grepArgs struct {
	Pattern string `json:"pattern"`
	Include string `json:"include"`
	Path    string `json:"path"`
}

func (t *GrepTool) Execute(ctx context.Context, raw json.RawMessage, env *Context) (Result, error) {
	var args grepArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Result{}, fmt.Errorf("grep: invalid arguments: %w", err)
	}
	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return Result{}, fmt.Errorf("grep: invalid pattern: %w", err)
	}

	root := resolvePath(env, args.Path)
	include := filepath.ToSlash(args.Include)

	var sb strings.Builder
	total := 0

	err = walkFiles(root, func(path string, info os.FileInfo) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if total >= maxGrepMatches {
			return nil
		}
		if info.Size() > maxFileSize {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if include != "" && !matchGlob(include, rel) && !matchGlob(include, filepath.Base(rel)) {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			if !re.MatchString(line) {
				continue
			}
			if len(line) > grepLineLimit {
				line = line[:grepLineLimit] + "..."
			}
			fmt.Fprintf(&sb, "%s:%d: %s\n", rel, lineNo, line)
			total++
			if total >= maxGrepMatches {
				break
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("grep: %w", err)
	}

	out := strings.TrimRight(sb.String(), "\n")
	if total == 0 {
		out = "No matches found"
	} else if total >= maxGrepMatches {
		out += "\n... truncated"
	}

	return Result{
		Title:  fmt.Sprintf("Grep %s", args.Pattern),
		Output: out,
		Metadata: map[string]any{
			"matches": total,
		},
	}, nil
}

func walkFiles(root string, fn func(path string, info os.FileInfo) error) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == ".subcoder" {
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		return fn(path, info)
	})
}

// matchGlob implements glob matching with ** support over slash-separated
// paths.
func matchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

func matchSegments(pattern, name []string) bool {
	for len(pattern) > 0 {
		segment := pattern[0]
		if segment == "**" {
			for skip := 0; skip <= len(name); skip++ {
				if matchSegments(pattern[1:], name[skip:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		matched, err := filepath.Match(segment, name[0])
		if err != nil || !matched {
			return false
		}
		pattern = pattern[1:]
		name = name[1:]
	}
	return len(name) == 0
}
