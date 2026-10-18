package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/arthurkushman/subcoder/internal/agent"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/spf13/cobra"
)

const workflowFile = `name: SubCoder

on:
  issues:
    types: [opened, edited]
  issue_comment:
    types: [created]
  pull_request:
    types: [opened, synchronize]

permissions:
  contents: write
  issues: write
  pull-requests: write

jobs:
  subcoder:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.24"
      - run: go install github.com/arthurkushman/subcoder@latest
      - name: Run SubCoder
        run: subcoder github run
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
`

func newGitHubCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Manage the GitHub Actions agent",
	}

	var force bool
	install := &cobra.Command{
		Use:   "install",
		Short: "Install a GitHub Actions workflow for SubCoder",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolveDirectory()
			if err != nil {
				return err
			}
			path := filepath.Join(dir, ".github", "workflows", "subcoder.yml")
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(workflowFile), 0o644); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Installed "+path)
			return nil
		},
	}
	install.Flags().BoolVar(&force, "force", false, "Overwrite an existing workflow")

	var eventPath string
	var promptText string
	run := &cobra.Command{
		Use:   "run",
		Short: "Run the agent for a GitHub event",
		RunE: func(cmd *cobra.Command, args []string) error {
			if eventPath == "" {
				eventPath = os.Getenv("GITHUB_EVENT_PATH")
			}
			prompt := promptText
			if prompt == "" {
				if eventPath == "" {
					return errors.New("no --event or GITHUB_EVENT_PATH provided")
				}
				data, err := os.ReadFile(eventPath)
				if err != nil {
					return err
				}
				prompt, err = promptFromEvent(data)
				if err != nil {
					return err
				}
			}

			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			app.Runner.Emit = newTextEmitter(cmd.OutOrStdout(), cmd.ErrOrStderr())
			agentDef := agent.Default(app.Agents)
			if globals.Agent != "" {
				found, ok := agent.Find(app.Agents, globals.Agent)
				if !ok {
					return fmt.Errorf("unknown agent %q", globals.Agent)
				}
				agentDef = found
			}
			sess := session.New(app.Directory)
			if err := app.Sessions.Save(sess); err != nil {
				return err
			}
			_, err = app.Runner.Run(context.Background(), agent.RunInput{
				Session: sess,
				Agent:   agentDef,
				Model:   app.Model,
				Text:    prompt,
			})
			fmt.Fprintln(cmd.OutOrStdout())
			return err
		},
	}
	run.Flags().StringVar(&eventPath, "event", "", "Path to a GitHub event JSON payload")
	run.Flags().StringVar(&promptText, "prompt", "", "Override the prompt derived from the event")

	cmd.AddCommand(install, run)
	return cmd
}

type githubEvent struct {
	Action string `json:"action"`
	Issue  *struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	} `json:"issue"`
	PullRequest *struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
	} `json:"pull_request"`
	Comment *struct {
		Body string `json:"body"`
	} `json:"comment"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func promptFromEvent(data []byte) (string, error) {
	var event githubEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return "", fmt.Errorf("parse GitHub event: %w", err)
	}

	var sb string
	if event.Repository.FullName != "" {
		sb += "Repository: " + event.Repository.FullName + "\n"
	}
	sb += "GitHub event action: " + event.Action + "\n"

	switch {
	case event.Issue != nil:
		sb += fmt.Sprintf("\nIssue #%d: %s\n\n%s\n", event.Issue.Number, event.Issue.Title, event.Issue.Body)
	case event.PullRequest != nil:
		sb += fmt.Sprintf("\nPull request #%d: %s\n\n%s\n", event.PullRequest.Number, event.PullRequest.Title, event.PullRequest.Body)
	}
	if event.Comment != nil && event.Comment.Body != "" {
		sb += "\nComment:\n" + event.Comment.Body + "\n"
	}

	sb += "\nInvestigate the repository and respond with a concise, helpful GitHub comment. Do not commit changes unless the request explicitly asks for them."
	return sb, nil
}
