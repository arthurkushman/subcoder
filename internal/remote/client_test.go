package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arthurkushman/subcoder/internal/session"
)

func TestClientFlow(t *testing.T) {
	var replied string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			fmt.Fprint(w, `{"status":"ok"}`)
		case r.URL.Path == "/sessions" && r.Method == http.MethodGet:
			fmt.Fprint(w, `{"sessions":[{"id":"ses_1","title":"one","directory":"/tmp"}]}`)
		case r.URL.Path == "/sessions" && r.Method == http.MethodPost:
			fmt.Fprint(w, `{"id":"ses_new","directory":"/tmp"}`)
		case r.URL.Path == "/sessions/ses_1/messages":
			w.Header().Set("Content-Type", "text/event-stream")
			write := func(event session.Event) {
				data, _ := json.Marshal(event)
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			write(session.Event{Type: session.EventPartUpdated, SessionID: "ses_1", Message: &session.Message{ID: "m1", Role: "assistant"}, Part: &session.Part{ID: "p1", Type: session.PartText, Text: "hello"}})
			write(session.Event{Type: session.EventSessionIdle, SessionID: "ses_1"})
		case strings.Contains(r.URL.Path, "/permissions/"):
			var input map[string]string
			_ = json.NewDecoder(r.Body).Decode(&input)
			replied = input["reply"]
			fmt.Fprint(w, `{"ok":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New(server.URL + "/")
	ctx := context.Background()
	if err := client.Health(ctx); err != nil {
		t.Fatal(err)
	}
	sessions, err := client.Sessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].ID != "ses_1" {
		t.Fatalf("sessions = %+v err = %v", sessions, err)
	}
	created, err := client.CreateSession(ctx, "/tmp")
	if err != nil || created.ID != "ses_new" {
		t.Fatalf("created = %+v err = %v", created, err)
	}
	if err := client.Reply(ctx, "ses_1", "per_1", "once"); err != nil {
		t.Fatal(err)
	}
	if replied != "once" {
		t.Fatalf("reply = %q", replied)
	}

	events, errs := client.Send(ctx, "ses_1", "hi")
	var sawText, sawIdle bool
	for event := range events {
		if event.Type == session.EventPartUpdated && event.Part != nil && event.Part.Text == "hello" {
			sawText = true
		}
		if event.Type == session.EventSessionIdle {
			sawIdle = true
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !sawText || !sawIdle {
		t.Fatalf("sawText=%v sawIdle=%v", sawText, sawIdle)
	}
}

func TestClientErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := New(server.URL)
	if err := client.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}
