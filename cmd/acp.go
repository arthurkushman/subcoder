package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/arthurkushman/subcoder/internal/acp"
	"github.com/spf13/cobra"
)

func newACPCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "acp",
		Short: "Start an Agent Client Protocol server over stdio",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return acp.Serve(ctx, acp.Options{
				Runner:     app.Runner,
				Sessions:   app.Sessions,
				Agents:     app.Agents,
				Permission: app.Permission,
				Directory:  app.Directory,
				Model:      app.Model,
				In:         cmd.InOrStdin(),
				Out:        cmd.OutOrStdout(),
			})
		},
	}
}
