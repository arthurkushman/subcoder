package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/arthurkushman/subcoder/internal/mcp"
	"github.com/arthurkushman/subcoder/internal/server"
	"github.com/arthurkushman/subcoder/internal/version"
	"github.com/spf13/cobra"
)

func newServeCommand() *cobra.Command {
	var hostname string
	var port int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the SubCoder HTTP server",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			httpServer := server.New(server.Options{
				Providers:  app.Providers,
				Sessions:   app.Sessions,
				Runner:     app.Runner,
				Agents:     app.Agents,
				Permission: app.Permission,
				Directory:  app.Directory,
			})

			addr := fmt.Sprintf("%s:%d", hostname, port)
			fmt.Fprintf(cmd.OutOrStdout(), "SubCoder %s listening on http://%s\n", version.Version, addr)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			errCh := make(chan error, 1)
			go func() {
				errCh <- server.ListenAndServe(ctx, addr, httpServer.Handler())
			}()

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				return nil
			}
		},
	}

	cmd.Flags().StringVar(&hostname, "hostname", "127.0.0.1", "Hostname to bind")
	cmd.Flags().IntVarP(&port, "port", "p", 4096, "Port to bind")
	return cmd
}

func newMCPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP servers",
	}

	list := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List configured MCP servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			out := cmd.OutOrStdout()
			if len(app.Config.MCP) == 0 {
				fmt.Fprintln(out, "No MCP servers configured.")
				return nil
			}
			names := make([]string, 0, len(app.Config.MCP))
			for name := range app.Config.MCP {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				server := app.Config.MCP[name]
				target := server.URL
				if target == "" && len(server.Command) > 0 {
					target = fmt.Sprintf("%v", server.Command)
				}
				enabled := "enabled"
				if !server.IsEnabled() {
					enabled = "disabled"
				}
				fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", name, server.Type, enabled, target)
			}
			return nil
		},
	}

	test := &cobra.Command{
		Use:   "test [name]",
		Short: "Start MCP servers and list their tools",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			servers := app.Config.MCP
			if len(args) == 1 {
				serverCfg, ok := servers[args[0]]
				if !ok {
					return fmt.Errorf("unknown MCP server %q", args[0])
				}
				servers = map[string]config.MCPServerConfig{args[0]: serverCfg}
			}

			manager, err := mcp.Start(cmd.Context(), servers)
			if manager != nil {
				defer manager.Close()
			}
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			for _, tool := range manager.Tools() {
				fmt.Fprintf(out, "%s\t%s\n", tool.Name(), tool.Description())
			}
			return nil
		},
	}

	var (
		name    string
		command []string
		url     string
		global  bool
		enabled bool
	)
	add := &cobra.Command{
		Use:   "add",
		Short: "Add an MCP server to the configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return errors.New("--name is required")
			}
			if url == "" && len(command) == 0 {
				return errors.New("either --command or --url is required")
			}

			target := config.GlobalPath()
			if !global {
				dir, err := resolveDirectory()
				if err != nil {
					return err
				}
				target = dir + "/subcoder.json"
			}

			cfg := config.New()
			if data, err := os.ReadFile(target); err == nil {
				parsed, err := config.Parse(data)
				if err != nil {
					return fmt.Errorf("parse %s: %w", target, err)
				}
				cfg = parsed
			}
			if cfg.MCP == nil {
				cfg.MCP = map[string]config.MCPServerConfig{}
			}

			serverCfg := config.MCPServerConfig{Type: "local", Command: command}
			if url != "" {
				serverCfg.Type = "remote"
				serverCfg.URL = url
			}
			serverCfg.Enabled = &enabled
			if _, exists := cfg.MCP[name]; exists {
				fmt.Fprintf(cmd.OutOrStdout(), "Overwriting existing MCP server %s\n", name)
			}
			cfg.MCP[name] = serverCfg

			if err := cfg.Save(target); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved MCP server %s to %s\n", name, target)
			return nil
		},
	}
	add.Flags().StringVar(&name, "name", "", "Server name")
	add.Flags().StringArrayVar(&command, "command", nil, "Command to run (repeat for arguments)")
	add.Flags().StringVar(&url, "url", "", "Remote MCP server URL")
	add.Flags().BoolVar(&global, "global", false, "Write to the global config")
	add.Flags().BoolVar(&enabled, "enabled", true, "Enable the server")

	var removeGlobal bool
	remove := &cobra.Command{
		Use:   "remove [name]",
		Short: "Remove an MCP server from the configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			removeName := name
			if removeName == "" && len(args) == 1 {
				removeName = args[0]
			}
			if removeName == "" {
				return errors.New("--name is required")
			}

			target := config.GlobalPath()
			if !removeGlobal {
				dir, err := resolveDirectory()
				if err != nil {
					return err
				}
				target = dir + "/subcoder.json"
			}

			data, err := os.ReadFile(target)
			if err != nil {
				return err
			}
			cfg, err := config.Parse(data)
			if err != nil {
				return err
			}
			delete(cfg.MCP, removeName)
			if err := cfg.Save(target); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed MCP server %s from %s\n", removeName, target)
			return nil
		},
	}
	remove.Flags().StringVar(&name, "name", "", "Server name")
	remove.Flags().BoolVar(&removeGlobal, "global", false, "Use the global config")

	cmd.AddCommand(list, test, add, remove)
	return cmd
}
