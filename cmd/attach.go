package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/remote"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/spf13/cobra"
)

func newAttachCommand() *cobra.Command {
	var sessionID string
	var continueSession bool
	var directory string
	var promptText string
	var autoApprove bool

	cmd := &cobra.Command{
		Use:   "attach [url]",
		Short: "Attach a terminal to a running SubCoder server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := remote.New(normalizeURL(args[0]))
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			if err := client.Health(ctx); err != nil {
				return fmt.Errorf("connect to %s: %w", client.BaseURL, err)
			}

			sess, err := attachSession(ctx, client, sessionID, continueSession, directory)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()
			fmt.Fprintf(errOut, "attached to %s · session %s\n", client.BaseURL, sess.ID)

			repl := &attachREPL{
				client:    client,
				ctx:       ctx,
				sessionID: sess.ID,
				in:        bufio.NewReader(cmd.InOrStdin()),
				out:       out,
				errOut:    errOut,
				auto:      autoApprove,
			}

			if promptText != "" {
				return repl.send(promptText, false)
			}
			return repl.loop()
		},
	}

	cmd.Flags().StringVarP(&sessionID, "session", "s", "", "Session ID to attach to")
	cmd.Flags().BoolVarP(&continueSession, "continue", "c", false, "Attach to the most recent session")
	cmd.Flags().StringVar(&directory, "dir", "", "Working directory for a new session")
	cmd.Flags().StringVarP(&promptText, "prompt", "p", "", "Send a single prompt and exit")
	cmd.Flags().BoolVar(&autoApprove, "auto", false, "Allow permission requests automatically")
	return cmd
}

func normalizeURL(url string) string {
	if strings.Contains(url, "://") {
		return url
	}
	return "http://" + url
}

func attachSession(ctx context.Context, client *remote.Client, sessionID string, continueSession bool, directory string) (*session.Session, error) {
	if sessionID != "" {
		return client.LoadSession(ctx, sessionID)
	}

	sessions, err := client.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	if continueSession || len(sessions) > 0 {
		for _, summary := range sessions {
			if directory == "" || summary.Directory == directory {
				return client.LoadSession(ctx, summary.ID)
			}
		}
	}
	if directory == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		directory = wd
	}
	return client.CreateSession(ctx, directory)
}

type attachREPL struct {
	client    *remote.Client
	ctx       context.Context
	sessionID string
	in        *bufio.Reader
	out       io.Writer
	errOut    io.Writer
	auto      bool
}

func (r *attachREPL) loop() error {
	fmt.Fprintln(r.errOut, "type /help for commands")
	for {
		fmt.Fprint(r.errOut, "> ")
		line, err := r.in.ReadString('\n')
		if err != nil && strings.TrimSpace(line) == "" {
			return nil
		}
		text := strings.TrimSpace(line)
		switch {
		case text == "":
			continue
		case text == "/exit" || text == "/quit":
			return nil
		case text == "/help":
			fmt.Fprintln(r.errOut, "/new /sessions /session <id> /interrupt /compact /exit")
		case text == "/new":
			sess, err := r.client.CreateSession(r.ctx, "")
			if err != nil {
				fmt.Fprintln(r.errOut, "error: "+err.Error())
				continue
			}
			r.sessionID = sess.ID
			fmt.Fprintln(r.errOut, "session: "+sess.ID)
		case text == "/sessions":
			sessions, err := r.client.Sessions(r.ctx)
			if err != nil {
				fmt.Fprintln(r.errOut, "error: "+err.Error())
				continue
			}
			for _, item := range sessions {
				marker := " "
				if item.ID == r.sessionID {
					marker = "*"
				}
				fmt.Fprintf(r.errOut, "%s %s  %s  %s\n", marker, item.ID, item.UpdatedAt, item.Title)
			}
		case strings.HasPrefix(text, "/session "):
			id := strings.TrimSpace(strings.TrimPrefix(text, "/session "))
			sess, err := r.client.LoadSession(r.ctx, id)
			if err != nil {
				fmt.Fprintln(r.errOut, "error: "+err.Error())
				continue
			}
			r.sessionID = sess.ID
			fmt.Fprintln(r.errOut, "session: "+sess.ID)
		case text == "/interrupt":
			if err := r.client.Interrupt(r.ctx, r.sessionID); err != nil {
				fmt.Fprintln(r.errOut, "error: "+err.Error())
			}
		case text == "/compact":
			if err := r.client.Compact(r.ctx, r.sessionID); err != nil {
				fmt.Fprintln(r.errOut, "error: "+err.Error())
			}
		case strings.HasPrefix(text, "/"):
			fmt.Fprintln(r.errOut, "unknown command "+text)
		default:
			if err := r.send(text, true); err != nil {
				fmt.Fprintln(r.errOut, "error: "+err.Error())
			}
		}
	}
}

func (r *attachREPL) send(text string, interactive bool) error {
	events, errs := r.client.Send(r.ctx, r.sessionID, text)
	emit := newTextEmitter(r.out, r.errOut)

	for event := range events {
		emit(event)
		switch event.Type {
		case session.EventPermission:
			r.replyPermission(event, interactive)
		case session.EventSessionError:
			return errors.New(event.Error)
		case session.EventSessionIdle:
			fmt.Fprintln(r.out)
			return nil
		}
	}
	if err := <-errs; err != nil {
		return err
	}
	fmt.Fprintln(r.out)
	return nil
}

func (r *attachREPL) replyPermission(event session.Event, interactive bool) {
	data, err := json.Marshal(event.Data)
	if err != nil {
		return
	}
	var req permission.Request
	if err := json.Unmarshal(data, &req); err != nil {
		return
	}

	reply := "reject"
	if r.auto {
		reply = "once"
	} else if interactive {
		fmt.Fprintf(r.errOut, "\npermission: %s [y] once [a] always [n] reject: ", req.Title)
		line, _ := r.in.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			reply = "once"
		case "a", "always":
			reply = "always"
		}
	}
	if err := r.client.Reply(r.ctx, r.sessionID, req.ID, reply); err != nil {
		fmt.Fprintln(r.errOut, "permission reply failed: "+err.Error())
	}
}
