package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/arthurkushman/subcoder/internal/rules"
	"github.com/spf13/cobra"
)

func newAgentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent",
		Short:   "Manage agents",
		Aliases: []string{"agents"},
	}

	var (
		name        string
		description string
		prompt      string
		model       string
		mode        string
		tools       string
		global      bool
		force       bool
	)

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a markdown agent definition",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			reader := bufio.NewReader(cmd.InOrStdin())

			if name == "" {
				fmt.Fprint(out, "Agent name: ")
				line, _ := reader.ReadString('\n')
				name = strings.TrimSpace(line)
			}
			if name == "" {
				return errors.New("agent name is required")
			}
			if description == "" {
				fmt.Fprint(out, "Description: ")
				line, _ := reader.ReadString('\n')
				description = strings.TrimSpace(line)
			}
			if prompt == "" {
				fmt.Fprint(out, "System prompt (single line, empty for default): ")
				line, _ := reader.ReadString('\n')
				prompt = strings.TrimSpace(line)
			}

			dir, err := resolveDirectory()
			if err != nil {
				return err
			}
			targetDir := filepath.Join(dir, ".subcoder", "agent")
			if global {
				targetDir = filepath.Join(config.ConfigDir(), "agent")
			}
			if err := os.MkdirAll(targetDir, 0o755); err != nil {
				return err
			}
			path := filepath.Join(targetDir, name+".md")
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}

			var sb strings.Builder
			sb.WriteString("---\n")
			fmt.Fprintf(&sb, "description: %s\n", description)
			if mode != "" {
				fmt.Fprintf(&sb, "mode: %s\n", mode)
			}
			if model != "" {
				fmt.Fprintf(&sb, "model: %s\n", model)
			}
			if tools != "" {
				fmt.Fprintf(&sb, "tools: [%s]\n", tools)
			}
			sb.WriteString("---\n")
			if prompt == "" {
				fmt.Fprintf(&sb, "You are the %s agent.\n", name)
			} else {
				sb.WriteString(prompt + "\n")
			}

			if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(out, "Created agent %s at %s\n", name, path)
			return nil
		},
	}
	create.Flags().StringVar(&name, "name", "", "Agent name")
	create.Flags().StringVar(&description, "description", "", "Short description")
	create.Flags().StringVar(&prompt, "prompt", "", "System prompt")
	create.Flags().StringVar(&model, "model", "", "Default model for this agent")
	create.Flags().StringVar(&mode, "mode", "subagent", "Agent mode: primary, subagent or all")
	create.Flags().StringVar(&tools, "tools", "", "Comma-separated tool allow-list")
	create.Flags().BoolVar(&global, "global", false, "Write to the global config directory")
	create.Flags().BoolVar(&force, "force", false, "Overwrite existing agent")

	list := &cobra.Command{
		Use:   "list",
		Short: "List agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			out := cmd.OutOrStdout()
			for _, agentDef := range app.Agents {
				mode := agentDef.Mode
				if mode == "" {
					mode = "all"
				}
				toolsInfo := "all tools"
				if len(agentDef.Tools) > 0 {
					toolsInfo = strings.Join(agentDef.Tools, ",")
				}
				model := agentDef.Model
				if model == "" {
					model = app.Model
				}
				fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", agentDef.Name, mode, model, toolsInfo, agentDef.Description)
			}
			return nil
		},
	}

	cmd.AddCommand(create, list)
	return cmd
}

func newInitCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create an AGENTS.md instruction file for the project",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveDirectory()
			if err != nil {
				return err
			}
			path := filepath.Join(dir, rules.FileName)
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			content := rules.Init(dir)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing AGENTS.md")
	return cmd
}
