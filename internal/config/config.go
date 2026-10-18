package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type ModelConfig struct {
	Name       string  `json:"name,omitempty"`
	Context    int     `json:"context,omitempty"`
	MaxOutput  int     `json:"maxOutput,omitempty"`
	Reasoning  bool    `json:"reasoning,omitempty"`
	InputCost  float64 `json:"inputCost,omitempty"`
	OutputCost float64 `json:"outputCost,omitempty"`
}

type ProviderConfig struct {
	Type    string                 `json:"type,omitempty"`
	APIKey  string                 `json:"apiKey,omitempty"`
	BaseURL string                 `json:"baseURL,omitempty"`
	Models  map[string]ModelConfig `json:"models,omitempty"`
}

type MCPServerConfig struct {
	Type        string            `json:"type,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	URL         string            `json:"url,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
}

func (m MCPServerConfig) IsEnabled() bool {
	return m.Enabled == nil || *m.Enabled
}

type HookConfig struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type AgentConfig struct {
	Description string   `json:"description,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	Model       string   `json:"model,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

type Config struct {
	Model        string                     `json:"model,omitempty"`
	SmallModel   string                     `json:"smallModel,omitempty"`
	Theme        string                     `json:"theme,omitempty"`
	AutoApprove  bool                       `json:"autoApprove,omitempty"`
	MaxSteps     int                        `json:"maxSteps,omitempty"`
	Temperature  *float64                   `json:"temperature,omitempty"`
	Providers    map[string]ProviderConfig  `json:"provider,omitempty"`
	Agents       map[string]AgentConfig     `json:"agent,omitempty"`
	Permissions  map[string]any             `json:"permission,omitempty"`
	MCP          map[string]MCPServerConfig `json:"mcp,omitempty"`
	Hooks        map[string][]HookConfig    `json:"hooks,omitempty"`
	Instructions []string                   `json:"instructions,omitempty"`
	Snapshots    *bool                      `json:"snapshots,omitempty"`

	GlobalPath  string `json:"-"`
	ProjectPath string `json:"-"`
	Directory   string `json:"-"`
}

const (
	DefaultMaxSteps = 50
)

func New() *Config {
	return &Config{
		Providers: map[string]ProviderConfig{},
		Agents:    map[string]AgentConfig{},
		MCP:       map[string]MCPServerConfig{},
	}
}

func ConfigDir() string {
	if v := os.Getenv("SUBCODER_CONFIG_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "subcoder")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".subcoder"
	}
	return filepath.Join(home, ".config", "subcoder")
}

func DataDir() string {
	if v := os.Getenv("SUBCODER_DATA_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "subcoder")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".subcoder-data"
	}
	return filepath.Join(home, ".local", "share", "subcoder")
}

func GlobalPath() string {
	if v := os.Getenv("SUBCODER_CONFIG"); v != "" {
		return v
	}
	return filepath.Join(ConfigDir(), "subcoder.json")
}

func projectCandidates(dir string) []string {
	return []string{
		filepath.Join(dir, "subcoder.jsonc"),
		filepath.Join(dir, "subcoder.json"),
		filepath.Join(dir, ".subcoder", "subcoder.jsonc"),
		filepath.Join(dir, ".subcoder", "subcoder.json"),
	}
}

func FindProjectConfig(dir string) string {
	cur := dir
	for {
		for _, candidate := range projectCandidates(cur) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

func Load(directory string) (*Config, error) {
	cfg := New()
	cfg.Directory = directory
	cfg.GlobalPath = GlobalPath()

	if data, err := os.ReadFile(cfg.GlobalPath); err == nil {
		parsed, err := Parse(data)
		if err != nil {
			return nil, err
		}
		cfg.merge(parsed)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if path := FindProjectConfig(directory); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		parsed, err := Parse(data)
		if err != nil {
			return nil, err
		}
		cfg.ProjectPath = path
		cfg.merge(parsed)
	}

	return cfg, nil
}

func Parse(data []byte) (*Config, error) {
	c := New()
	if err := json.Unmarshal(StripJSONC(data), c); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) Save(path string) error {
	if path == "" {
		path = c.GlobalPath
	}
	if path == "" {
		path = GlobalPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func (c *Config) Steps() int {
	if c.MaxSteps > 0 {
		return c.MaxSteps
	}
	return DefaultMaxSteps
}

// SnapshotsEnabled reports whether git-backed file snapshots are enabled.
// They are on by default and can be disabled with "snapshots": false.
func (c *Config) SnapshotsEnabled() bool {
	return c.Snapshots == nil || *c.Snapshots
}

func (c *Config) merge(src *Config) {
	if src.Model != "" {
		c.Model = src.Model
	}
	if src.SmallModel != "" {
		c.SmallModel = src.SmallModel
	}
	if src.Theme != "" {
		c.Theme = src.Theme
	}
	if src.AutoApprove {
		c.AutoApprove = true
	}
	if src.MaxSteps > 0 {
		c.MaxSteps = src.MaxSteps
	}
	if src.Temperature != nil {
		c.Temperature = src.Temperature
	}
	if src.Snapshots != nil {
		c.Snapshots = src.Snapshots
	}
	if len(src.Instructions) > 0 {
		c.Instructions = append(c.Instructions, src.Instructions...)
	}

	c.Providers = mergeProviders(c.Providers, src.Providers)
	c.Agents = mergeAgents(c.Agents, src.Agents)
	c.MCP = mergeMCP(c.MCP, src.MCP)
	c.Hooks = mergeHooks(c.Hooks, src.Hooks)
	if c.Permissions == nil && src.Permissions != nil {
		c.Permissions = map[string]any{}
	}
	for tool, rule := range src.Permissions {
		c.Permissions[tool] = rule
	}
}

func mergeProviders(dst, src map[string]ProviderConfig) map[string]ProviderConfig {
	if dst == nil {
		dst = map[string]ProviderConfig{}
	}
	for name, provider := range src {
		existing, ok := dst[name]
		if !ok {
			dst[name] = provider
			continue
		}
		if provider.Type != "" {
			existing.Type = provider.Type
		}
		if provider.APIKey != "" {
			existing.APIKey = provider.APIKey
		}
		if provider.BaseURL != "" {
			existing.BaseURL = provider.BaseURL
		}
		if existing.Models == nil {
			existing.Models = map[string]ModelConfig{}
		}
		for id, model := range provider.Models {
			existing.Models[id] = model
		}
		dst[name] = existing
	}
	return dst
}

func mergeAgents(dst, src map[string]AgentConfig) map[string]AgentConfig {
	if dst == nil {
		dst = map[string]AgentConfig{}
	}
	for name, agent := range src {
		existing, ok := dst[name]
		if !ok {
			dst[name] = agent
			continue
		}
		if agent.Description != "" {
			existing.Description = agent.Description
		}
		if agent.Mode != "" {
			existing.Mode = agent.Mode
		}
		if agent.Model != "" {
			existing.Model = agent.Model
		}
		if agent.Prompt != "" {
			existing.Prompt = agent.Prompt
		}
		if len(agent.Tools) > 0 {
			existing.Tools = agent.Tools
		}
		if agent.Temperature != nil {
			existing.Temperature = agent.Temperature
		}
		dst[name] = existing
	}
	return dst
}

func mergeHooks(dst, src map[string][]HookConfig) map[string][]HookConfig {
	if dst == nil {
		dst = map[string][]HookConfig{}
	}
	for event, hooks := range src {
		dst[event] = append(append([]HookConfig{}, dst[event]...), hooks...)
	}
	return dst
}

func mergeMCP(dst, src map[string]MCPServerConfig) map[string]MCPServerConfig {
	if dst == nil {
		dst = map[string]MCPServerConfig{}
	}
	for name, server := range src {
		dst[name] = server
	}
	return dst
}

func StripJSONC(data []byte) []byte {
	stripped := stripComments(data)
	return stripTrailingCommas(stripped)
}

func stripComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(data); i++ {
		ch := data[i]

		if inLineComment {
			if ch == '\n' {
				inLineComment = false
				out = append(out, ch)
			}
			continue
		}
		if inBlockComment {
			if ch == '*' && i+1 < len(data) && data[i+1] == '/' {
				inBlockComment = false
				i++
			} else if ch == '\n' {
				out = append(out, ch)
			}
			continue
		}
		if inString {
			out = append(out, ch)
			if ch == '\\' && i+1 < len(data) {
				out = append(out, data[i+1])
				i++
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch {
		case ch == '"':
			inString = true
			out = append(out, ch)
		case ch == '/' && i+1 < len(data) && data[i+1] == '/':
			inLineComment = true
			i++
		case ch == '/' && i+1 < len(data) && data[i+1] == '*':
			inBlockComment = true
			i++
		default:
			out = append(out, ch)
		}
	}

	return out
}

func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false

	for i := 0; i < len(data); i++ {
		ch := data[i]
		if inString {
			out = append(out, ch)
			if ch == '\\' && i+1 < len(data) {
				out = append(out, data[i+1])
				i++
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		if ch == '"' {
			inString = true
			out = append(out, ch)
			continue
		}
		if ch == ',' {
			j := i + 1
			for j < len(data) && unicode.IsSpace(rune(data[j])) {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, ch)
	}

	return out
}

func EnvKey(provider string) string {
	name := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(provider))
	return "SUBCODER_" + name + "_API_KEY"
}
