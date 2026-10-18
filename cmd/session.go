package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/spf13/cobra"
)

func newSessionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "session",
		Short:   "Manage sessions",
		Aliases: []string{"sessions"},
	}

	var format string
	var maxCount int

	list := &cobra.Command{
		Use:   "list",
		Short: "List sessions, most recent first",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			out := cmd.OutOrStdout()
			summaries, err := app.Sessions.List()
			if err != nil {
				return err
			}
			if maxCount > 0 && len(summaries) > maxCount {
				summaries = summaries[:maxCount]
			}
			if format == "json" {
				if summaries == nil {
					summaries = []session.Summary{}
				}
				data, err := json.MarshalIndent(summaries, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(data))
				return nil
			}
			if format != "" && format != "table" {
				return fmt.Errorf("unknown format %q (use table or json)", format)
			}
			if len(summaries) == 0 {
				fmt.Fprintln(out, "No sessions yet.")
				return nil
			}
			for _, summary := range summaries {
				marker := " "
				if summary.Directory == app.Directory {
					marker = "*"
				}
				title := summary.Title
				if title == "" {
					title = "(untitled)"
				}
				fmt.Fprintf(out, "%s %s\t%s\t%s\t%s\n", marker, summary.ID, summary.UpdatedAt, summary.Model, title)
			}
			return nil
		},
	}
	list.Flags().StringVar(&format, "format", "table", "Output format: table or json")
	list.Flags().IntVarP(&maxCount, "max-count", "n", 0, "Limit to the N most recent sessions")

	var title, model, agentName string
	newCmd := &cobra.Command{
		Use:   "new",
		Short: "Create a new session",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			sess := session.New(app.Directory)
			sess.Title = title
			sess.Model = model
			sess.Agent = agentName
			if err := app.Sessions.Save(sess); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), sess.ID)
			return nil
		},
	}
	newCmd.Flags().StringVar(&title, "title", "", "Session title")
	newCmd.Flags().StringVar(&model, "model", "", "Model to pin to the session")
	newCmd.Flags().StringVar(&agentName, "agent", "", "Agent to pin to the session")

	show := &cobra.Command{
		Use:   "show [session-id]",
		Short: "Show a session as JSON (defaults to the latest in this directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			var sess *session.Session
			if len(args) == 1 {
				sess, err = app.Sessions.Load(args[0])
			} else {
				sess, err = app.Sessions.Latest(app.Directory)
				if errors.Is(err, os.ErrNotExist) {
					return errors.New("no sessions found for this directory")
				}
			}
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(sess, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}

	deleteCmd := &cobra.Command{
		Use:   "delete [session-id]",
		Short: "Delete a session (defaults to the latest in this directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			id := ""
			if len(args) == 1 {
				id = args[0]
			} else {
				sess, err := app.Sessions.Latest(app.Directory)
				if errors.Is(err, os.ErrNotExist) {
					return errors.New("no sessions found for this directory")
				}
				if err != nil {
					return err
				}
				id = sess.ID
			}
			if err := app.Sessions.Delete(id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted session %s\n", id)
			return nil
		},
	}

	cmd.AddCommand(list, newCmd, show, deleteCmd)
	return cmd
}

func newExportCommand() *cobra.Command {
	var output string
	var sanitize bool
	cmd := &cobra.Command{
		Use:   "export [session-id]",
		Short: "Export a session as JSON (defaults to the latest in this directory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			var sess *session.Session
			if len(args) == 1 {
				sess, err = app.Sessions.Load(args[0])
			} else {
				sess, err = app.Sessions.Latest(app.Directory)
				if errors.Is(err, os.ErrNotExist) {
					return errors.New("no sessions found for this directory")
				}
			}
			if err != nil {
				return err
			}

			exported := sess
			if sanitize {
				exported = sanitizeSession(sess)
			}
			data, err := json.MarshalIndent(exported, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')

			if output == "" {
				_, err = cmd.OutOrStdout().Write(data)
				return err
			}
			if err := os.WriteFile(output, data, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Exported session %s to %s\n", sess.ID, output)
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Write to a file instead of stdout")
	cmd.Flags().BoolVar(&sanitize, "sanitize", false, "Redact message content and tool output")
	return cmd
}

func sanitizeSession(sess *session.Session) *session.Session {
	clone := *sess
	clone.Title = "[redacted]"
	clone.Messages = make([]session.Message, len(sess.Messages))
	for i := range sess.Messages {
		message := *sess.Messages[i].Clone()
		for j := range message.Parts {
			part := &message.Parts[j]
			if part.Text != "" {
				part.Text = "[redacted]"
			}
			if part.Tool != nil {
				part.Tool.Title = "[redacted]"
				part.Tool.Arguments = "[redacted]"
				part.Tool.Output = "[redacted]"
				part.Tool.Error = "[redacted]"
				part.Tool.Metadata = nil
			}
		}
		message.Error = ""
		clone.Messages[i] = message
	}
	clone.Todos = make([]session.Todo, len(sess.Todos))
	for i, todo := range sess.Todos {
		clone.Todos[i] = session.Todo{Content: "[redacted]", Status: todo.Status, Priority: todo.Priority}
	}
	return &clone
}

func newImportCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file>",
		Short: "Import a session from an exported JSON file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			var sess session.Session
			if err := json.Unmarshal(data, &sess); err != nil {
				return fmt.Errorf("invalid session file: %w", err)
			}
			if sess.ID == "" {
				sess.ID = session.New(app.Directory).ID
			}
			if sess.Directory == "" {
				sess.Directory = app.Directory
			}
			if sess.CreatedAt.IsZero() {
				sess.CreatedAt = time.Now().UTC()
			}
			sess.UpdatedAt = time.Now().UTC()
			if err := app.Sessions.Save(&sess); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Imported session %s\n", sess.ID)
			return nil
		},
	}
}

func newStatsCommand() *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show aggregate usage statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			summaries, err := app.Sessions.List()
			if err != nil {
				return err
			}

			cutoff := time.Time{}
			if days > 0 {
				cutoff = time.Now().AddDate(0, 0, -days)
			}

			type modelStats struct {
				sessions int
				input    float64
				output   float64
				cost     float64
			}
			byModel := map[string]*modelStats{}
			total := modelStats{}

			for _, summary := range summaries {
				updated, _ := time.Parse("2006-01-02T15:04:05Z", summary.UpdatedAt)
				if !cutoff.IsZero() && updated.Before(cutoff) {
					continue
				}
				sess, err := app.Sessions.Load(summary.ID)
				if err != nil {
					continue
				}
				model := sess.Model
				if model == "" {
					model = "(unknown)"
				}
				stats, ok := byModel[model]
				if !ok {
					stats = &modelStats{}
					byModel[model] = stats
				}
				stats.sessions++
				stats.input += sess.Usage.Input
				stats.output += sess.Usage.Output
				stats.cost += sess.Usage.Cost
				total.sessions++
				total.input += sess.Usage.Input
				total.output += sess.Usage.Output
				total.cost += sess.Usage.Cost
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Sessions: %d\n", total.sessions)
			fmt.Fprintf(out, "Tokens:   input=%d output=%d\n", int(total.input), int(total.output))
			fmt.Fprintf(out, "Cost:     $%.4f\n", total.cost)

			if len(byModel) > 1 {
				fmt.Fprintln(out, "\nBy model:")
				names := make([]string, 0, len(byModel))
				for name := range byModel {
					names = append(names, name)
				}
				sortStrings(names)
				for _, name := range names {
					stats := byModel[name]
					fmt.Fprintf(out, "  %-40s sessions=%d in=%d out=%d cost=$%.4f\n",
						name, stats.sessions, int(stats.input), int(stats.output), stats.cost)
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&days, "days", 0, "Only include sessions updated in the last N days")
	return cmd
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if strings.Compare(values[j], values[i]) < 0 {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
