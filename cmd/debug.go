package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/spf13/cobra"
)

func newDebugCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "debug",
		Short: "Debugging and troubleshooting tools",
	}

	paths := &cobra.Command{
		Use:   "paths",
		Short: "Print configuration and data paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveDirectory()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			project := config.FindProjectConfig(dir)
			if project == "" {
				project = "(none)"
			}
			fmt.Fprintf(out, "directory:      %s\n", dir)
			fmt.Fprintf(out, "config file:    %s\n", config.GlobalPath())
			fmt.Fprintf(out, "config dir:     %s\n", config.ConfigDir())
			fmt.Fprintf(out, "data dir:       %s\n", config.DataDir())
			fmt.Fprintf(out, "sessions dir:   %s\n", config.DataDir()+string(os.PathSeparator)+"sessions")
			fmt.Fprintf(out, "project config: %s\n", project)
			return nil
		},
	}

	configCommand := &cobra.Command{
		Use:   "config",
		Short: "Print the merged configuration with secrets redacted",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveDirectory()
			if err != nil {
				return err
			}
			cfg, err := config.Load(dir)
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(redactConfig(cfg), "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}

	envCommand := &cobra.Command{
		Use:   "env",
		Short: "Print SubCoder and provider environment variables with secrets redacted",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			var lines []string
			for _, entry := range os.Environ() {
				key, value, ok := strings.Cut(entry, "=")
				if !ok {
					continue
				}
				upper := strings.ToUpper(key)
				if !strings.HasPrefix(upper, "SUBCODER_") &&
					!strings.HasPrefix(upper, "OPENAI_") &&
					!strings.HasPrefix(upper, "ANTHROPIC_") &&
					!strings.HasPrefix(upper, "GITHUB_") {
					continue
				}
				if isSecretKey(upper) && value != "" {
					value = "***"
				}
				lines = append(lines, key+"="+value)
			}
			sort.Strings(lines)
			for _, line := range lines {
				fmt.Fprintln(out, line)
			}
			return nil
		},
	}

	providers := &cobra.Command{
		Use:   "providers",
		Short: "List configured providers and models",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()
			out := cmd.OutOrStdout()
			models := app.Providers.Models(cmd.Context())
			for _, id := range app.Providers.IDs() {
				fmt.Fprintf(out, "%s\n", id)
				for _, model := range models {
					if model.Provider == id {
						fmt.Fprintf(out, "  %s\n", model.FullID())
					}
				}
			}
			return nil
		},
	}

	cmd.AddCommand(paths, configCommand, envCommand, providers)
	return cmd
}

func redactConfig(cfg *config.Config) *config.Config {
	copied := *cfg
	copied.Providers = make(map[string]config.ProviderConfig, len(cfg.Providers))
	for name, provider := range cfg.Providers {
		if provider.APIKey != "" {
			provider.APIKey = "***"
		}
		copied.Providers[name] = provider
	}
	return &copied
}

func isSecretKey(key string) bool {
	return strings.Contains(key, "KEY") || strings.Contains(key, "TOKEN") || strings.Contains(key, "SECRET") || strings.Contains(key, "PASSWORD")
}
