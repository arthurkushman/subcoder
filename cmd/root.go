package cmd

import (
	"fmt"
	"os"

	"github.com/arthurkushman/subcoder/internal/tui"
	"github.com/arthurkushman/subcoder/internal/version"
	"github.com/spf13/cobra"
)

func Execute() error {
	err := NewRootCommand().Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
	}
	return err
}

type tuiFlags struct {
	Continue  bool
	SessionID string
	Prompt    string
	Fork      bool
}

func (f *tuiFlags) register(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&f.Continue, "continue", "c", false, "Continue the most recent session in this directory")
	cmd.Flags().StringVarP(&f.SessionID, "session", "s", "", "Session ID to continue")
	cmd.Flags().StringVar(&f.Prompt, "prompt", "", "Prompt to send immediately")
	cmd.Flags().BoolVar(&f.Fork, "fork", false, "Fork the session when continuing")
}

func NewRootCommand() *cobra.Command {
	var flags tuiFlags

	root := &cobra.Command{
		Use:           "subcoder [project]",
		Short:         "SubCoder is an open AI-agent for coding tasks",
		Long:          "SubCoder is an open AI-agent that drives different LLMs for coding tasks from the terminal.",
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				globals.Directory = args[0]
			}
			return launchTUI(cmd, flags)
		},
	}

	root.PersistentFlags().StringVar(&globals.Directory, "directory", "", "Project directory (defaults to the current working directory)")
	root.PersistentFlags().StringVarP(&globals.Model, "model", "m", "", "Model to use, e.g. anthropic/claude-sonnet-4-5")
	root.PersistentFlags().StringVarP(&globals.Agent, "agent", "a", "", "Agent to use, e.g. build or plan")
	root.PersistentFlags().BoolVar(&globals.AutoApprove, "auto-approve", false, "Automatically approve all tool permissions")
	root.PersistentFlags().BoolVar(&globals.PrintLogs, "print-logs", false, "Print debug logs to stderr")
	root.PersistentFlags().StringVar(&globals.LogLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flags.register(root)

	root.AddCommand(
		newRunCommand(),
		newTUICommand(),
		newServeCommand(),
		newWebCommand(),
		newAttachCommand(),
		newACPCommand(),
		newAuthCommand(),
		newModelsCommand(),
		newAgentCommand(),
		newCommandsCommand(),
		newSessionCommand(),
		newExportCommand(),
		newImportCommand(),
		newMCPCommand(),
		newStatsCommand(),
		newInitCommand(),
		newGitHubCommand(),
		newPRCommand(),
		newDebugCommand(),
		newDBCommand(),
		newUpgradeCommand(),
		newUninstallCommand(),
		newVersionCommand(),
	)

	return root
}

func launchTUI(cmd *cobra.Command, flags tuiFlags) error {
	app, err := buildApp(cmd)
	if err != nil {
		return err
	}
	defer app.Close()

	return tui.Run(tui.Options{
		Config:      app.Config,
		Providers:   app.Providers,
		Sessions:    app.Sessions,
		Runner:      app.Runner,
		Agents:      app.Agents,
		Permission:  app.Permission,
		Snapshots:   app.Snapshots,
		Directory:   app.Directory,
		Model:       app.Model,
		Agent:       globals.Agent,
		Prompt:      flags.Prompt,
		SessionID:   flags.SessionID,
		Continue:    flags.Continue,
		Fork:        flags.Fork,
		AutoApprove: app.AutoApprove,
	})
}

func newTUICommand() *cobra.Command {
	var flags tuiFlags
	cmd := &cobra.Command{
		Use:   "tui [project]",
		Short: "Start the interactive terminal UI",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				globals.Directory = args[0]
			}
			return launchTUI(cmd, flags)
		},
	}
	flags.register(cmd)
	return cmd
}
