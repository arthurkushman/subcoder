package permission

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/arthurkushman/subcoder/internal/id"
)

type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
)

type Request struct {
	ID        string
	SessionID string
	Tool      string
	Pattern   string
	Title     string
	Details   string
}

type Reply struct {
	Allowed bool
	Always  bool
}

type Asker interface {
	Ask(ctx context.Context, req Request) (Reply, error)
}

type DeniedError struct {
	Tool    string
	Pattern string
	Reason  string
}

func (e *DeniedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("permission denied for %s (%s): %s", e.Tool, e.Pattern, e.Reason)
	}
	return fmt.Sprintf("permission denied for %s (%s)", e.Tool, e.Pattern)
}

var defaultDecisions = map[string]Decision{
	"read":      Allow,
	"glob":      Allow,
	"grep":      Allow,
	"ls":        Allow,
	"webfetch":  Ask,
	"todowrite": Allow,
	"bash":      Ask,
	"write":     Ask,
	"edit":      Ask,
	"task":      Ask,
}

type Engine struct {
	mu          sync.Mutex
	rules       map[string]any
	asker       Asker
	autoApprove bool
	remembered  map[string]bool
}

func New(rules map[string]any, asker Asker, autoApprove bool) *Engine {
	if rules == nil {
		rules = map[string]any{}
	}
	return &Engine{
		rules:       rules,
		asker:       asker,
		autoApprove: autoApprove,
		remembered:  map[string]bool{},
	}
}

func (e *Engine) SetAsker(asker Asker) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.asker = asker
}

// Decision resolves the configured decision for a tool invocation without
// consulting the asker.
func (e *Engine) Decision(tool, value string) Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.decisionLocked(tool, value)
}

func (e *Engine) decisionLocked(tool, value string) Decision {
	if e.remembered[rememberKey(tool, value)] {
		return Allow
	}

	if rule, ok := e.rules[tool]; ok {
		if decision, matched := resolveRule(rule, value); matched {
			return decision
		}
	}
	if rule, ok := e.rules["*"]; ok {
		if decision, matched := resolveRule(rule, value); matched {
			return decision
		}
	}
	if decision, ok := defaultDecisions[tool]; ok {
		return decision
	}
	return Ask
}

func resolveRule(rule any, value string) (Decision, bool) {
	switch typed := rule.(type) {
	case string:
		return normalizeDecision(typed), true
	case map[string]any:
		type match struct {
			pattern  string
			decision Decision
		}
		var matches []match
		for pattern, raw := range typed {
			decision, ok := raw.(string)
			if !ok {
				continue
			}
			if pattern == "*" || matchPattern(pattern, value) {
				matches = append(matches, match{pattern: pattern, decision: normalizeDecision(decision)})
			}
		}
		if len(matches) == 0 {
			return "", false
		}
		sort.SliceStable(matches, func(i, j int) bool {
			return len(matches[i].pattern) > len(matches[j].pattern)
		})
		return matches[0].decision, true
	case json.RawMessage:
		var decoded any
		if err := json.Unmarshal(typed, &decoded); err == nil {
			return resolveRule(decoded, value)
		}
	case map[string]string:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[key] = item
		}
		return resolveRule(converted, value)
	}
	return "", false
}

func normalizeDecision(value string) Decision {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "allow", "always", "yes", "true":
		return Allow
	case "deny", "never", "no", "false":
		return Deny
	default:
		return Ask
	}
}

func matchPattern(pattern, value string) bool {
	if strings.Contains(pattern, "/") {
		matched, err := filepath.Match(pattern, value)
		if err == nil && matched {
			return true
		}
	}
	expr := "^" + regexp.QuoteMeta(pattern) + "$"
	expr = strings.ReplaceAll(expr, `\*`, `.*`)
	expr = strings.ReplaceAll(expr, `\?`, `.`)
	re, err := regexp.Compile(expr)
	if err != nil {
		return false
	}
	return re.MatchString(value)
}

func rememberKey(tool, value string) string {
	return tool + "\x00" + value
}

// Check resolves permissions for a tool invocation, optionally asking the
// user. It returns a *DeniedError when the invocation is not allowed.
func (e *Engine) Check(ctx context.Context, req Request) error {
	if req.ID == "" {
		req.ID = id.New("per")
	}

	decision := e.Decision(req.Tool, req.Pattern)
	switch decision {
	case Allow:
		return nil
	case Deny:
		return &DeniedError{Tool: req.Tool, Pattern: req.Pattern, Reason: "denied by configuration"}
	}

	e.mu.Lock()
	autoApprove := e.autoApprove
	asker := e.asker
	e.mu.Unlock()

	if autoApprove {
		return nil
	}
	if asker == nil {
		return &DeniedError{Tool: req.Tool, Pattern: req.Pattern, Reason: "non-interactive session"}
	}

	reply, err := asker.Ask(ctx, req)
	if err != nil {
		return &DeniedError{Tool: req.Tool, Pattern: req.Pattern, Reason: err.Error()}
	}
	if !reply.Allowed {
		return &DeniedError{Tool: req.Tool, Pattern: req.Pattern, Reason: "rejected by user"}
	}
	if reply.Always {
		e.mu.Lock()
		e.remembered[rememberKey(req.Tool, req.Pattern)] = true
		e.mu.Unlock()
	}
	return nil
}
