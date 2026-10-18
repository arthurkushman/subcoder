package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/arthurkushman/subcoder/internal/server"
	"github.com/spf13/cobra"
)

func newWebCommand() *cobra.Command {
	var hostname string
	var port int
	var noOpen bool

	cmd := &cobra.Command{
		Use:   "web",
		Short: "Start the HTTP server with a browser UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := buildApp(cmd)
			if err != nil {
				return err
			}
			defer app.Close()

			httpServer := server.New(server.Options{
				Providers:  app.Providers,
				Sessions:   app.Sessions,
				Runner:     app.Runner,
				Agents:     app.Agents,
				Permission: app.Permission,
				Directory:  app.Directory,
				WebUI:      true,
			})

			addr := fmt.Sprintf("%s:%d", hostname, port)
			url := fmt.Sprintf("http://%s", addr)
			fmt.Fprintf(cmd.OutOrStdout(), "SubCoder web UI listening on %s\n", url)
			if !noOpen {
				openBrowser(url)
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return server.ListenAndServe(ctx, addr, httpServer.Handler())
		},
	}

	cmd.Flags().StringVar(&hostname, "hostname", "127.0.0.1", "Hostname to bind")
	cmd.Flags().IntVarP(&port, "port", "p", 4096, "Port to bind")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "Do not open a browser")
	return cmd
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
