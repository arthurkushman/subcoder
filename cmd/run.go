package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/arthurkushman/subcoder/internal/agent"
	"github.com/arthurkushman/subcoder/internal/command"
	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/prompt"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/spf13/cobra"
)

func newRunCommand() *cobra.Command {
	var format string
	var continueSession bool
	var sessionID string
	var commandName string
	var files []string
	var title string
	var fork bool

	cmd := &cobra.Command{
		Use:   "run [message...]",
		Short: "Run the agent non-interactively and print the result",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			promptText := strings.Join(args, " ")
			commandAgent := ""
			commandModel := ""
			if commandName != "" {
				custom, ok := command.Find(command.Load(app.Directory), commandName)
				if !ok {
					return fmt.Errorf("unknown command %q", commandName)
				}
				if promptText == "" {
					piped, err := io.ReadAll(os.Stdin)
					if err != nil {
						return err
					}
					promptText = strings.TrimSpace(string(piped))
				}
				expanded, err := command.Expand(custom, promptText, app.Directory)
				if err != nil {
					return err
				}
				promptText = expanded
				commandAgent = custom.Agent
				commandModel = custom.Model
			}
			if promptText == "" {
				piped, err := io.ReadAll(os.Stdin)
				if err != nil {
					return err
				}
				promptText = strings.TrimSpace(string(piped))
			}
			if promptText == "" {
				return errors.New("no message provided; pass it as an argument or on stdin")
			}

			// When --command is used its expansion is the prompt; file mentions
			// in a plain prompt are attached as context either way.
			var attachments []string
			var included []string
			if commandName == "" {
				var blocks string
				blocks, included = prompt.FileContext(promptText, app.Directory)
				if blocks != "" {
					attachments = append(attachments, blocks)
				}
			}
			for _, file := range files {
				block, rel, err := prompt.FileBlock(app.Directory, file)
				if err != nil {
					return fmt.Errorf("attach %s: %w", file, err)
				}
				attachments = append(attachments, block)
				included = append(included, rel)
			}
			contextText := strings.Join(attachments, "\n\n")
			if len(included) > 0 {
				logf("attached: %s", strings.Join(included, ", "))
			}

			var sess *session.Session
			switch {
			case sessionID != "":
				sess, err = app.Sessions.Load(sessionID)
				if err != nil {
					return fmt.Errorf("load session %s: %w", sessionID, err)
				}
			case continueSession:
				sess, err = app.Sessions.Latest(app.Directory)
				if err != nil {
					if !errors.Is(err, os.ErrNotExist) {
						return err
					}
					sess = session.New(app.Directory)
				}
			default:
				sess = session.New(app.Directory)
			}
			if fork && (sessionID != "" || continueSession) {
				sess = forkSession(sess)
			}
			if title != "" {
				sess.Title = title
			}

			agentName := globals.Agent
			if agentName == "" {
				agentName = commandAgent
			}
			agentDef := agent.Default(app.Agents)
			if agentName != "" {
				found, ok := agent.Find(app.Agents, agentName)
				if !ok {
					return fmt.Errorf("unknown agent %q", agentName)
				}
				agentDef = found
			}

			modelID := app.Model
			if globals.Model == "" && commandModel != "" {
				modelID = commandModel
			}

			if !app.AutoApprove && stdinIsTerminal() {
				app.Permission.SetAsker(&cliAsker{in: os.Stdin, out: cmd.ErrOrStderr()})
			}

			out := cmd.OutOrStdout()
			switch format {
			case "text", "":
				app.Runner.Emit = newTextEmitter(out, cmd.ErrOrStderr())
			case "json":
				app.Runner.Emit = newJSONEmitter(out)
			default:
				return fmt.Errorf("unknown format %q (use text or json)", format)
			}

			message, err := app.Runner.Run(context.Background(), agent.RunInput{
				Session: sess,
				Agent:   agentDef,
				Model:   modelID,
				Text:    promptText,
				Context: contextText,
			})

			if format == "json" {
				result := map[string]any{
					"type":      "result",
					"sessionID": sess.ID,
					"error":     nil,
				}
				if message != nil {
					result["text"] = message.Text()
				}
				if err != nil {
					result["error"] = err.Error()
				}
				data, _ := json.Marshal(result)
				fmt.Fprintln(out, string(data))
			} else {
				fmt.Fprintln(out)
			}

			if err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "text", "Output format: text or json")
	cmd.Flags().BoolVarP(&continueSession, "continue", "c", false, "Continue the most recent session in this directory")
	cmd.Flags().StringVarP(&sessionID, "session", "s", "", "Session ID to continue")
	cmd.Flags().StringVar(&commandName, "command", "", "Run a custom command from .subcoder/command")
	cmd.Flags().StringArrayVarP(&files, "file", "f", nil, "Attach a file to the prompt (repeatable)")
	cmd.Flags().StringVar(&title, "title", "", "Title for the session")
	cmd.Flags().BoolVar(&fork, "fork", false, "Fork the session when continuing")

	return cmd
}

func forkSession(sess *session.Session) *session.Session {
	forked := session.New(sess.Directory)
	forked.Title = sess.Title
	forked.Model = sess.Model
	forked.Agent = sess.Agent
	forked.Todos = append([]session.Todo(nil), sess.Todos...)
	forked.ParentID = sess.ID
	for i := range sess.Messages {
		forked.Messages = append(forked.Messages, *sess.Messages[i].Clone())
	}
	return forked
}

func newTextEmitter(out, errOut io.Writer) session.Emitter {
	printed := map[string]int{}
	running := map[string]bool{}

	return func(event session.Event) {
		switch event.Type {
		case session.EventPartUpdated:
			if event.Part == nil || event.Message == nil || event.Message.Role != "assistant" {
				return
			}
			part := event.Part
			switch part.Type {
			case session.PartText:
				text := part.Text
				if len(text) > printed[part.ID] {
					fmt.Fprint(out, text[printed[part.ID]:])
					printed[part.ID] = len(text)
				}
			case session.PartTool:
				if part.Tool == nil {
					return
				}
				switch part.Tool.State {
				case session.ToolRunning:
					title := part.Tool.Title
					if title == "" {
						title = part.Tool.Name
						if args := strings.TrimSpace(part.Tool.Arguments); args != "" && args != "{}" {
							title += " " + args
						}
					}
					fmt.Fprintf(errOut, "\n> %s\n", firstLine(title))
					running[part.Tool.ID] = true
				case session.ToolCompleted:
					if running[part.Tool.ID] {
						output := firstLine(part.Tool.Output)
						fmt.Fprintf(errOut, "  done: %s\n", output)
					}
				case session.ToolError:
					fmt.Fprintf(errOut, "  error: %s\n", part.Tool.Error)
				}
			}
		case session.EventSessionError:
			fmt.Fprintf(errOut, "\nerror: %s\n", event.Error)
		}
	}
}

func newJSONEmitter(out io.Writer) session.Emitter {
	return func(event session.Event) {
		data, err := json.Marshal(event)
		if err != nil {
			return
		}
		fmt.Fprintln(out, string(data))
	}
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	if len(text) > 120 {
		text = text[:120]
	}
	return text
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

type cliAsker struct {
	in  io.Reader
	out io.Writer
}

func (a *cliAsker) Ask(ctx context.Context, req permission.Request) (permission.Reply, error) {
	fmt.Fprintf(a.out, "\nPermission required: %s\nDetails: %s\nAllow? [y]es / [n]o / [a]lways: ", req.Title, req.Details)
	reader := bufio.NewReader(a.in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return permission.Reply{}, errors.New("no input")
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return permission.Reply{Allowed: true}, nil
	case "a", "always":
		return permission.Reply{Allowed: true, Always: true}, nil
	default:
		return permission.Reply{Allowed: false}, nil
	}
}
