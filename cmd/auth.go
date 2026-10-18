package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/arthurkushman/subcoder/internal/auth"
	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/spf13/cobra"
)

func newAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage provider credentials",
	}

	var provider, key string
	login := &cobra.Command{
		Use:   "login",
		Short: "Store an API key for a provider",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			reader := bufio.NewReader(cmd.InOrStdin())

			if provider == "" {
				fmt.Fprint(out, "Provider (openai, anthropic, ...): ")
				line, err := reader.ReadString('\n')
				if err != nil && line == "" {
					return errors.New("provider is required")
				}
				provider = strings.TrimSpace(line)
			}
			if key == "" {
				fmt.Fprint(out, "API key: ")
				line, err := reader.ReadString('\n')
				if err != nil && line == "" {
					return errors.New("api key is required")
				}
				key = strings.TrimSpace(line)
			}
			if provider == "" || key == "" {
				return errors.New("provider and key are required")
			}

			store := auth.NewStore(config.DataDir())
			if err := store.Set(provider, auth.Entry{Type: "api", Key: key}); err != nil {
				return err
			}
			fmt.Fprintf(out, "Stored credentials for %s in %s\n", provider, store.Path())
			return nil
		},
	}
	login.Flags().StringVar(&provider, "provider", "", "Provider name")
	login.Flags().StringVar(&key, "key", "", "API key")

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List stored credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			store := auth.NewStore(config.DataDir())
			entries, err := store.List()
			if err != nil {
				return err
			}

			names := make([]string, 0, len(entries))
			for name := range entries {
				names = append(names, name)
			}
			sort.Strings(names)

			if len(names) == 0 {
				fmt.Fprintln(out, "No credentials stored. Use `subcoder auth login`.")
				return nil
			}
			for _, name := range names {
				fmt.Fprintf(out, "%s\t%s\n", name, maskKey(entries[name].Key))
			}
			return nil
		},
	}

	cmd.AddCommand(login, list)

	var logoutProvider string
	logout := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials for a provider",
		RunE: func(cmd *cobra.Command, args []string) error {
			if logoutProvider == "" && len(args) > 0 {
				logoutProvider = args[0]
			}
			if logoutProvider == "" {
				return errors.New("--provider is required")
			}
			store := auth.NewStore(config.DataDir())
			if err := store.Delete(logoutProvider); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed credentials for %s\n", logoutProvider)
			return nil
		},
	}
	logout.Flags().StringVar(&logoutProvider, "provider", "", "Provider name")

	cmd.AddCommand(logout)
	return cmd
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

func newModelsCommand() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "models [provider]",
		Short: "List available models",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			out := cmd.OutOrStdout()
			models := app.Providers.Models(cmd.Context())
			filter := ""
			if len(args) == 1 {
				filter = args[0]
			}

			count := 0
			for _, model := range models {
				if filter != "" && model.Provider != filter {
					continue
				}
				if verbose {
					fmt.Fprintf(out, "%s/%s\t%s\tctx=%d\tmaxOut=%d\tin=$%.4f/1M\tout=$%.4f/1M\n",
						model.Provider, model.ID, model.Name, model.Context, model.MaxOutput, model.InputCost, model.OutputCost)
				} else {
					fmt.Fprintf(out, "%s/%s\t%s\n", model.Provider, model.ID, model.Name)
				}
				count++
			}
			if count == 0 {
				fmt.Fprintln(out, "No models found. Configure providers in subcoder.json or run `subcoder auth login`.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "Include context limits and per-token costs")
	return cmd
}
