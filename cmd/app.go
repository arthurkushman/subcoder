package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arthurkushman/subcoder/internal/agent"
	"github.com/arthurkushman/subcoder/internal/auth"
	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/arthurkushman/subcoder/internal/hook"
	"github.com/arthurkushman/subcoder/internal/mcp"
	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/provider/anthropic"
	"github.com/arthurkushman/subcoder/internal/provider/mock"
	"github.com/arthurkushman/subcoder/internal/provider/openai"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/arthurkushman/subcoder/internal/skill"
	"github.com/arthurkushman/subcoder/internal/snapshot"
	"github.com/arthurkushman/subcoder/internal/tool"
	"github.com/spf13/cobra"
)

type globalOptions struct {
	Directory   string
	Model       string
	Agent       string
	AutoApprove bool
	PrintLogs   bool
	LogLevel    string
}

var globals globalOptions

type App struct {
	Config      *config.Config
	Auth        *auth.Store
	Providers   *provider.Registry
	Sessions    *session.Store
	Tools       *tool.Registry
	Permission  *permission.Engine
	Runner      *agent.Runner
	Agents      []agent.Agent
	Skills      []skill.Skill
	MCP         *mcp.Manager
	Snapshots   *snapshot.Manager
	Directory   string
	Model       string
	AutoApprove bool
}

func (a *App) Close() {
	if a.MCP != nil {
		a.MCP.Close()
	}
}

func logf(format string, args ...any) {
	if globals.PrintLogs {
		fmt.Fprintf(os.Stderr, "[subcoder] "+format+"\n", args...)
	}
}

func resolveDirectory() (string, error) {
	dir := globals.Directory
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	return filepath.Abs(dir)
}

func buildApp(cmd *cobra.Command) (*App, error) {
	dir, err := resolveDirectory()
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	authStore := auth.NewStore("")
	providers := provider.NewRegistry()

	names := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		pc := cfg.Providers[name]
		key := authStore.Key(name, pc)
		models := convertModels(name, pc.Models)

		switch {
		case pc.Type == "mock" || name == "mock":
			mockProvider := mock.New(name, models...)
			providers.Register(mockProvider)
		case pc.Type == "anthropic" || name == "anthropic":
			providers.Register(anthropic.New(anthropic.Options{
				ID:      name,
				BaseURL: pc.BaseURL,
				APIKey:  key,
				Models:  models,
			}))
		default:
			providers.Register(openai.New(openai.Options{
				ID:      name,
				BaseURL: pc.BaseURL,
				APIKey:  key,
				Models:  models,
			}))
		}
	}

	if os.Getenv("SUBCODER_MOCK") == "1" {
		if _, ok := providers.Get("mock"); !ok {
			providers.Register(mock.New("mock"))
		}
	}

	model := globals.Model
	if model == "" {
		model = cfg.Model
	}
	if model == "" {
		model = firstAvailableModel(providers, cfg)
	}
	providers.SetDefault(model)

	sessions := session.NewStore(config.DataDir())

	engine := permission.New(cfg.Permissions, nil, globals.AutoApprove || cfg.AutoApprove)

	runner := agent.NewRunner(providers, nil, sessions, engine)
	runner.Agents = agent.Load(cfg, dir)
	runner.Skills = skill.Load(dir)
	runner.Hooks = hook.New(cfg.Hooks)
	runner.Instructions = cfg.Instructions
	runner.DefaultModel = model
	runner.MaxSteps = cfg.Steps()

	tools := tool.NewRegistry(
		tool.NewRead(),
		tool.NewWrite(),
		tool.NewEdit(),
		tool.NewBash(),
		tool.NewGlob(),
		tool.NewGrep(),
		tool.NewLs(),
		tool.NewWebFetch(),
		tool.NewTodo(),
		tool.NewTask(runner),
	)
	if len(runner.Skills) > 0 {
		tools.Add(tool.NewSkill(runner.Skills))
	}

	var manager *mcp.Manager
	if len(cfg.MCP) > 0 {
		manager, err = mcp.Start(context.Background(), cfg.MCP)
		if err != nil {
			logf("mcp: %v", err)
		}
		if manager != nil {
			for _, mcpTool := range manager.Tools() {
				tools.Add(mcpTool)
				logf("mcp tool registered: %s", mcpTool.Name())
			}
		}
	}

	runner.Tools = tools

	var snapshots *snapshot.Manager
	if cfg.SnapshotsEnabled() {
		snapshots, err = snapshot.New(dir, config.DataDir())
		if err != nil {
			logf("snapshots: %v", err)
			snapshots = nil
		}
	}

	app := &App{
		Config:      cfg,
		Auth:        authStore,
		Providers:   providers,
		Sessions:    sessions,
		Tools:       tools,
		Permission:  engine,
		Runner:      runner,
		Agents:      runner.Agents,
		Skills:      runner.Skills,
		MCP:         manager,
		Snapshots:   snapshots,
		Directory:   dir,
		Model:       model,
		AutoApprove: globals.AutoApprove || cfg.AutoApprove,
	}
	return app, nil
}

func convertModels(providerName string, models map[string]config.ModelConfig) []provider.Model {
	builtin := map[string]provider.Model{}
	for _, m := range provider.BuiltinModels[providerName] {
		builtin[m.ID] = m
	}

	if len(models) == 0 {
		return provider.BuiltinModels[providerName]
	}

	ids := make([]string, 0, len(models))
	for id := range models {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	converted := make([]provider.Model, 0, len(ids))
	for _, id := range ids {
		mc := models[id]
		m := builtin[id]
		m.ID = id
		m.Provider = providerName
		if mc.Name != "" {
			m.Name = mc.Name
		} else if m.Name == "" {
			m.Name = id
		}
		if mc.Context > 0 {
			m.Context = mc.Context
		}
		if mc.MaxOutput > 0 {
			m.MaxOutput = mc.MaxOutput
		}
		if mc.InputCost > 0 {
			m.InputCost = mc.InputCost
		}
		if mc.OutputCost > 0 {
			m.OutputCost = mc.OutputCost
		}
		converted = append(converted, m)
	}
	return converted
}

func firstAvailableModel(providers *provider.Registry, cfg *config.Config) string {
	for _, id := range providers.IDs() {
		if builtin, ok := provider.BuiltinModels[id]; ok && len(builtin) > 0 {
			return builtin[0].FullID()
		}
		if pc, ok := cfg.Providers[id]; ok {
			for modelID := range pc.Models {
				return id + "/" + modelID
			}
		}
	}
	return ""
}

func formatUsage(usage session.Usage) string {
	var parts []string
	if usage.Input > 0 {
		parts = append(parts, fmt.Sprintf("in=%d", int(usage.Input)))
	}
	if usage.Output > 0 {
		parts = append(parts, fmt.Sprintf("out=%d", int(usage.Output)))
	}
	if usage.Cost > 0 {
		parts = append(parts, fmt.Sprintf("cost=$%.4f", usage.Cost))
	}
	if len(parts) == 0 {
		return "no usage recorded"
	}
	return strings.Join(parts, " ")
}
