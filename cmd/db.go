package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/spf13/cobra"
)

func newDBCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Inspect the local session store",
	}

	path := &cobra.Command{
		Use:   "path",
		Short: "Print the session store path",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), filepath.Join(config.DataDir(), "sessions"))
			return nil
		},
	}
	cmd.AddCommand(path)
	return cmd
}
