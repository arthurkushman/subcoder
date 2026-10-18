package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/arthurkushman/subcoder/internal/version"
	"github.com/spf13/cobra"
)

const modulePath = "github.com/arthurkushman/subcoder"

func newUpgradeCommand() *cobra.Command {
	var check bool
	var dryRun bool
	var method string

	cmd := &cobra.Command{
		Use:   "upgrade [version]",
		Short: "Upgrade SubCoder to the latest or a specific version",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()

			target := "latest"
			if len(args) == 1 && args[0] != "" {
				target = args[0]
				if target[0] >= '0' && target[0] <= '9' {
					target = "v" + target
				}
			}

			if check {
				latest, err := latestVersion(cmd.Context())
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "current: %s\nlatest:  %s\n", version.Version, latest)
				if strings.TrimPrefix(latest, "v") == strings.TrimPrefix(version.Version, "v") {
					fmt.Fprintln(out, "SubCoder is up to date.")
				} else {
					fmt.Fprintln(out, "A newer version is available.")
				}
				return nil
			}

			if method != "go" {
				return fmt.Errorf("unsupported upgrade method %q (supported: go)", method)
			}
			if _, err := exec.LookPath("go"); err != nil {
				return fmt.Errorf("go toolchain not found; install from https://go.dev/dl or use your package manager")
			}

			argsInstall := []string{"install", modulePath + "@" + target}
			if dryRun {
				fmt.Fprintln(out, "go "+strings.Join(argsInstall, " "))
				return nil
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Minute)
			defer cancel()
			process := exec.CommandContext(ctx, "go", argsInstall...)
			process.Stdout = out
			process.Stderr = cmd.ErrOrStderr()
			process.Stdin = os.Stdin
			if err := process.Run(); err != nil {
				return fmt.Errorf("go install: %w", err)
			}
			fmt.Fprintln(out, "Upgraded to "+target+".")
			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Check for the latest version without upgrading")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the upgrade command without running it")
	cmd.Flags().StringVar(&method, "method", "go", "Installation method (go)")
	return cmd
}

func latestVersion(ctx context.Context) (string, error) {
	url := os.Getenv("SUBCODER_UPGRADE_URL")
	if url == "" {
		url = "https://api.github.com/repos/arthurkushman/subcoder/releases/latest"
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "subcoder")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("version check failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	if release.TagName == "" {
		return "", fmt.Errorf("version check failed: empty tag")
	}
	return release.TagName, nil
}
