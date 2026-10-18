package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arthurkushman/subcoder/internal/session"
)

func TestAttachOneShot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case r.URL.Path == "/sessions/ses_1":
			fmt.Fprint(w, `{"id":"ses_1","directory":"/tmp","messages":[]}`)
		case r.URL.Path == "/sessions/ses_1/messages":
			w.Header().Set("Content-Type", "text/event-stream")
			write := func(event session.Event) {
				data, _ := json.Marshal(event)
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			write(session.Event{
				Type:      session.EventPartUpdated,
				SessionID: "ses_1",
				Message:   &session.Message{ID: "m1", Role: "assistant"},
				Part:      &session.Part{ID: "p1", Type: session.PartText, Text: "remote answer"},
			})
			write(session.Event{Type: session.EventSessionIdle, SessionID: "ses_1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	env := setupEnv(t, `{}`)
	out, _, err := execute(t, "attach", server.URL, "--session", "ses_1", "--prompt", "hi", "--directory", env.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "remote answer") {
		t.Fatalf("out = %q", out)
	}
}

func TestNormalizeURL(t *testing.T) {
	if got := normalizeURL("localhost:4096"); got != "http://localhost:4096" {
		t.Fatalf("got = %q", got)
	}
	if got := normalizeURL("https://example.com"); got != "https://example.com" {
		t.Fatalf("got = %q", got)
	}
}
