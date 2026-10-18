package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/spf13/cobra"
)

func newUninstallCommand() *cobra.Command {
	var keepConfig bool
	var keepData bool
	var dryRun bool
	var force bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove SubCoder configuration, session data and the binary",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			executable, _ := os.Executable()

			type target struct {
				label string
				path  string
				keep  bool
			}
			targets := []target{
				{label: "configuration", path: config.ConfigDir(), keep: keepConfig},
				{label: "session data", path: config.DataDir(), keep: keepData},
				{label: "binary", path: executable},
			}

			var remove []target
			for _, item := range targets {
				if item.keep || item.path == "" {
					continue
				}
				remove = append(remove, item)
			}

			for _, item := range remove {
				prefix := "remove"
				if dryRun {
					prefix = "would remove"
				}
				fmt.Fprintf(out, "%s %s: %s\n", prefix, item.label, item.path)
			}
			if dryRun {
				return nil
			}

			if !force {
				fmt.Fprint(cmd.ErrOrStderr(), "Remove the paths above? [y/N] ")
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if strings.ToLower(strings.TrimSpace(line)) != "y" {
					fmt.Fprintln(out, "Cancelled.")
					return nil
				}
			}

			for _, item := range remove {
				var err error
				if item.label == "binary" {
					err = os.Remove(item.path)
				} else {
					err = os.RemoveAll(item.path)
				}
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "failed to remove %s: %v\n", item.path, err)
					continue
				}
				fmt.Fprintf(out, "Removed %s: %s\n", item.label, item.path)
			}
			if !keepConfig {
				fmt.Fprintln(out, "Project-level .subcoder directories were left untouched.")
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&keepConfig, "keep-config", "c", false, "Keep configuration files")
	cmd.Flags().BoolVarP(&keepData, "keep-data", "d", false, "Keep session data and snapshots")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be removed without removing it")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip the confirmation prompt")
	return cmd
}
