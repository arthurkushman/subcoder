package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/arthurkushman/subcoder/internal/config"
)

const (
	ModePrimary  = "primary"
	ModeSubagent = "subagent"
	ModeAll      = "all"
)

const basePrompt = `You are SubCoder, an AI coding agent running in the user's terminal.

You help with software engineering tasks: writing and editing code, running commands, searching codebases, and debugging.

Guidelines:
- Be concise and direct. Answer briefly unless detail is requested.
- Prefer editing existing files over creating new ones.
- Never add comments unless asked.
- Follow the existing conventions of the codebase you work in.
- Use the todowrite tool to plan multi-step work.
- Use the task tool to delegate complex research to subagents.
- Use the skill tool to load a skill when the task matches one listed under Available skills.
- Verify your work: run builds and tests when possible.
- Never commit changes unless explicitly asked.`

type Agent struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	Model       string   `json:"model,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

func (a Agent) IsPrimary() bool {
	return a.Mode == "" || a.Mode == ModePrimary || a.Mode == ModeAll
}

func (a Agent) IsSubagent() bool {
	return a.Mode == "" || a.Mode == ModeSubagent || a.Mode == ModeAll
}

func Builtins() []Agent {
	return []Agent{
		{
			Name:        "build",
			Description: "Default agent for building, editing and running code",
			Mode:        ModePrimary,
			Prompt:      basePrompt,
		},
		{
			Name:        "plan",
			Description: "Read-only agent that analyzes code and proposes plans",
			Mode:        ModePrimary,
			Prompt: basePrompt + `

You are in plan mode: you must not change files or execute mutating commands.
Analyze the codebase, gather context, and produce a clear step-by-step plan.`,
			Tools: []string{"read", "glob", "grep", "ls", "webfetch", "todowrite", "task"},
		},
		{
			Name:        "general",
			Description: "General-purpose subagent for multi-step tasks",
			Mode:        ModeSubagent,
			Prompt:      basePrompt + "\n\nYou are a subagent: complete the delegated task and report back with a concise result.",
		},
		{
			Name:        "explore",
			Description: "Fast read-only subagent for codebase exploration",
			Mode:        ModeSubagent,
			Prompt:      "You are an exploration subagent. Search the codebase and answer questions precisely with file references. Never modify files.",
			Tools:       []string{"read", "glob", "grep", "ls"},
		},
	}
}

// Load merges built-in agents with configured and on-disk agent definitions.
// Markdown agents live in <project>/.subcoder/agent/*.md and the global
// config directory.
func Load(cfg *config.Config, directory string) []Agent {
	byName := map[string]Agent{}
	var order []string

	add := func(a Agent) {
		if _, exists := byName[a.Name]; !exists {
			order = append(order, a.Name)
		}
		byName[a.Name] = a
	}

	for _, a := range Builtins() {
		add(a)
	}

	for name, agentCfg := range cfg.Agents {
		agent := byName[name]
		agent.Name = name
		if agentCfg.Description != "" {
			agent.Description = agentCfg.Description
		}
		if agentCfg.Mode != "" {
			agent.Mode = agentCfg.Mode
		}
		if agentCfg.Model != "" {
			agent.Model = agentCfg.Model
		}
		if agentCfg.Prompt != "" {
			agent.Prompt = agentCfg.Prompt
		}
		if len(agentCfg.Tools) > 0 {
			agent.Tools = agentCfg.Tools
		}
		agent.Temperature = agentCfg.Temperature
		add(agent)
	}

	for _, dir := range []string{
		filepath.Join(directory, ".subcoder", "agent"),
		filepath.Join(config.ConfigDir(), "agent"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var names []string
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			agent := parseMarkdownAgent(strings.TrimSuffix(name, ".md"), string(data))
			existing := byName[agent.Name]
			if existing.Name != "" && agent.Prompt == "" {
				continue
			}
			add(agent)
		}
	}

	agents := make([]Agent, 0, len(order))
	for _, name := range order {
		agents = append(agents, byName[name])
	}
	return agents
}

func parseMarkdownAgent(name, content string) Agent {
	agent := Agent{Name: name}

	if strings.HasPrefix(content, "---\n") {
		rest := content[len("---\n"):]
		if idx := strings.Index(rest, "\n---"); idx >= 0 {
			frontmatter := rest[:idx]
			content = strings.TrimSpace(rest[idx+len("\n---"):])
			for _, line := range strings.Split(frontmatter, "\n") {
				key, value, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				key = strings.TrimSpace(key)
				value = strings.TrimSpace(value)
				switch key {
				case "description":
					agent.Description = value
				case "mode":
					agent.Mode = value
				case "model":
					agent.Model = value
				case "temperature":
					if parsed, err := strconv.ParseFloat(value, 64); err == nil {
						agent.Temperature = &parsed
					}
				case "tools":
					value = strings.Trim(value, "[]")
					for _, item := range strings.Split(value, ",") {
						item = strings.TrimSpace(item)
						if item != "" {
							agent.Tools = append(agent.Tools, item)
						}
					}
				}
			}
		}
	}

	agent.Prompt = strings.TrimSpace(content)
	if agent.Prompt == "" {
		agent.Prompt = fmt.Sprintf("You are the %s agent.", name)
	}
	return agent
}

func Find(agents []Agent, name string) (Agent, bool) {
	for _, agent := range agents {
		if agent.Name == name {
			return agent, true
		}
	}
	return Agent{}, false
}

func Default(agents []Agent) Agent {
	if agent, ok := Find(agents, "build"); ok {
		return agent
	}
	for _, agent := range agents {
		if agent.IsPrimary() {
			return agent
		}
	}
	if len(agents) > 0 {
		return agents[0]
	}
	return Agent{Name: "build", Prompt: basePrompt}
}
