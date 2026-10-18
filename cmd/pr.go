package cmd

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

func newPRCommand() *cobra.Command {
	var flags tuiFlags

	cmd := &cobra.Command{
		Use:   "pr <number>",
		Short: "Check out a GitHub pull request and start the TUI",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveDirectory()
			if err != nil {
				return err
			}
			if err := checkoutPR(context.Background(), dir, args[0]); err != nil {
				return err
			}
			globals.Directory = dir
			return launchTUI(cmd, flags)
		},
	}
	flags.register(cmd)
	return cmd
}

func checkoutPR(ctx context.Context, directory, number string) error {
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("the GitHub CLI (gh) is required for `subcoder pr`")
	}
	cmd := exec.CommandContext(ctx, "gh", "pr", "checkout", number)
	cmd.Dir = directory
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh pr checkout %s: %w: %s", number, err, strings.TrimSpace(string(output)))
	}
	return nil
}
