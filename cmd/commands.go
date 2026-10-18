package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/arthurkushman/subcoder/internal/command"
	"github.com/spf13/cobra"
)

func newCommandsCommand() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:     "commands",
		Aliases: []string{"command"},
		Short:   "List custom markdown commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveDirectory()
			if err != nil {
				return err
			}
			commands := command.Load(dir)
			out := cmd.OutOrStdout()

			if jsonOut {
				data, err := json.MarshalIndent(commands, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}

			if len(commands) == 0 {
				fmt.Fprintln(out, "No custom commands. Add markdown files in .subcoder/command/.")
				return nil
			}
			for _, custom := range commands {
				description := custom.Description
				if description == "" {
					description = custom.Path
				}
				fmt.Fprintf(out, "/%-14s %s\n", custom.Name, description)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print commands as JSON")
	return cmd
}
