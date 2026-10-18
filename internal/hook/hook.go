package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/arthurkushman/subcoder/internal/config"
)

// Event describes something that happened in the agent lifecycle. Hooks
// receive it as JSON on stdin and through SUBCODER_* environment variables.
type Event struct {
	Name      string `json:"event"`
	SessionID string `json:"sessionID,omitempty"`
	Directory string `json:"directory,omitempty"`
	Agent     string `json:"agent,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

type hook struct {
	command string
	timeout time.Duration
}

// Engine runs configured lifecycle hooks. A nil engine is a no-op.
type Engine struct {
	hooks map[string][]hook
}

// New builds an engine from the configuration. Commands run through sh -c in
// the event's directory.
func New(configured map[string][]config.HookConfig) *Engine {
	if len(configured) == 0 {
		return nil
	}
	engine := &Engine{hooks: map[string][]hook{}}
	for name, entries := range configured {
		for _, entry := range entries {
			command := strings.TrimSpace(entry.Command)
			if command == "" {
				continue
			}
			timeout := time.Duration(entry.Timeout) * time.Second
			if timeout <= 0 {
				timeout = 10 * time.Second
			}
			engine.hooks[name] = append(engine.hooks[name], hook{command: command, timeout: timeout})
		}
	}
	if len(engine.hooks) == 0 {
		return nil
	}
	return engine
}

// Has reports whether at least one hook is configured for the event.
func (e *Engine) Has(name string) bool {
	if e == nil {
		return false
	}
	return len(e.hooks[name]) > 0
}

// Run executes all hooks for the event. The first failing hook stops the
// chain and returns an error carrying its output.
func (e *Engine) Run(ctx context.Context, event Event) error {
	if e == nil {
		return nil
	}
	entries := e.hooks[event.Name]
	if len(entries) == 0 {
		return nil
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := entry.run(ctx, event, payload); err != nil {
			return err
		}
	}
	return nil
}

func (h hook) run(ctx context.Context, event Event, payload []byte) error {
	runCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-c", h.command)
	cmd.Dir = event.Directory
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	cmd.Env = append(os.Environ(),
		"SUBCODER_EVENT="+event.Name,
		"SUBCODER_SESSION_ID="+event.SessionID,
		"SUBCODER_DIRECTORY="+event.Directory,
		"SUBCODER_AGENT="+event.Agent,
		"SUBCODER_TOOL="+event.Tool,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("hook %s failed: %s", event.Name, message)
	}
	return nil
}
