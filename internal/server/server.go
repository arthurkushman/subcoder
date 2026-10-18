package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/arthurkushman/subcoder/internal/agent"
	"github.com/arthurkushman/subcoder/internal/id"
	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/session"
)

func ListenAndServe(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type Bus struct {
	mu          sync.Mutex
	next        int
	subscribers map[int]chan session.Event
}

func NewBus() *Bus {
	return &Bus{subscribers: map[int]chan session.Event{}}
}

func (b *Bus) Subscribe() (int, <-chan session.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	subID := b.next
	ch := make(chan session.Event, 256)
	b.subscribers[subID] = ch
	return subID, ch
}

func (b *Bus) Unsubscribe(id int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(ch)
	}
}

func (b *Bus) Publish(event session.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
}

type pending struct {
	ch chan permission.Reply
}

type Server struct {
	providers  *provider.Registry
	sessions   *session.Store
	runner     *agent.Runner
	agents     []agent.Agent
	permission *permission.Engine
	bus        *Bus
	token      string
	webUI      bool
	directory  string

	mu      sync.Mutex
	pending map[string]*pending
}

type Options struct {
	Providers  *provider.Registry
	Sessions   *session.Store
	Runner     *agent.Runner
	Agents     []agent.Agent
	Permission *permission.Engine
	Token      string
	WebUI      bool
	Directory  string
}

func New(opts Options) *Server {
	s := &Server{
		providers:  opts.Providers,
		sessions:   opts.Sessions,
		runner:     opts.Runner,
		agents:     opts.Agents,
		permission: opts.Permission,
		bus:        NewBus(),
		token:      opts.Token,
		webUI:      opts.WebUI,
		directory:  opts.Directory,
		pending:    map[string]*pending{},
	}
	if s.token == "" {
		s.token = os.Getenv("SUBCODER_SERVER_TOKEN")
	}
	if s.runner != nil {
		s.runner.Emit = s.bus.Publish
	}
	if s.permission != nil {
		s.permission.SetAsker(s)
	}
	return s
}

func (s *Server) Bus() *Bus {
	return s.bus
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	if s.webUI {
		mux.HandleFunc("GET /", s.handleIndex)
	}

	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /version", s.handleHealth)
	mux.HandleFunc("GET /models", s.handleModels)
	mux.HandleFunc("GET /agents", s.handleAgents)
	mux.HandleFunc("GET /sessions", s.handleListSessions)
	mux.HandleFunc("POST /sessions", s.handleCreateSession)
	mux.HandleFunc("GET /sessions/{id}", s.handleGetSession)
	mux.HandleFunc("DELETE /sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("POST /sessions/{id}/messages", s.handleMessage)
	mux.HandleFunc("POST /sessions/{id}/compact", s.handleCompact)
	mux.HandleFunc("POST /sessions/{id}/interrupt", s.handleInterrupt)
	mux.HandleFunc("POST /sessions/{id}/permissions/{reqID}", s.handlePermissionReply)
	mux.HandleFunc("GET /sessions/{id}/export", s.handleExport)
	mux.HandleFunc("GET /event", s.handleEventStream)

	return s.withAuth(mux)
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			header := r.Header.Get("Authorization")
			if header != "Bearer "+s.token {
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(webIndex))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	models := s.providers.Models(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"agents": s.agents})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.sessions.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": list})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Directory string `json:"directory"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
		Title     string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if input.Directory == "" {
		input.Directory = s.directory
	}
	if input.Directory == "" {
		writeError(w, http.StatusBadRequest, errors.New("directory is required"))
		return
	}

	sess := session.New(input.Directory)
	sess.Model = input.Model
	sess.Agent = input.Agent
	sess.Title = input.Title
	if err := s.sessions.Save(sess); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) loadSession(w http.ResponseWriter, r *http.Request) (*session.Session, bool) {
	sess, err := s.sessions.Load(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("session not found"))
		return nil, false
	}
	return sess, true
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.loadSession(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := s.sessions.Delete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("id")})
}

func (s *Server) handleMessage(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.loadSession(w, r)
	if !ok {
		return
	}

	var input struct {
		Text  string `json:"text"`
		Agent string `json:"agent"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if input.Text == "" {
		writeError(w, http.StatusBadRequest, errors.New("text is required"))
		return
	}

	agentDef := agent.Default(s.agents)
	if input.Agent != "" {
		if found, ok := agent.Find(s.agents, input.Agent); ok {
			agentDef = found
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	subID, events := s.bus.Subscribe()
	defer s.bus.Unsubscribe(subID)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := s.runner.Run(ctx, agent.RunInput{
			Session: sess,
			Agent:   agentDef,
			Model:   input.Model,
			Text:    input.Text,
		})
		done <- err
	}()

	enc := json.NewEncoder(w)
	emit := func(event session.Event) {
		fmt.Fprint(w, "data: ")
		_ = enc.Encode(event)
		fmt.Fprint(w, "\n")
		flusher.Flush()
	}

	for {
		select {
		case event := <-events:
			if event.SessionID != sess.ID {
				continue
			}
			emit(event)
			if event.Type == session.EventSessionIdle {
				return
			}
		case err := <-done:
			if err != nil {
				emit(session.Event{Type: session.EventSessionError, SessionID: sess.ID, Error: err.Error()})
			}
			emit(session.Event{Type: session.EventSessionIdle, SessionID: sess.ID})
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.loadSession(w, r)
	if !ok {
		return
	}
	var input struct {
		Model string `json:"model"`
	}
	_ = json.NewDecoder(r.Body).Decode(&input)

	message, err := s.runner.Compact(r.Context(), sess, input.Model)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, message)
}

func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	interrupted := s.runner.Interrupt(r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"interrupted": interrupted})
}

func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	subID, events := s.bus.Subscribe()
	defer s.bus.Unsubscribe(subID)

	enc := json.NewEncoder(w)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			fmt.Fprint(w, "data: ")
			_ = enc.Encode(event)
			fmt.Fprint(w, "\n")
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.loadSession(w, r)
	if !ok {
		return
	}
	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sess.ID+".json"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(data, '\n'))
}

// Ask implements permission.Asker: it publishes a permission request event and
// waits for a reply from the permissions endpoint.
func (s *Server) Ask(ctx context.Context, req permission.Request) (permission.Reply, error) {
	entry := &pending{ch: make(chan permission.Reply, 1)}

	s.mu.Lock()
	if req.ID == "" {
		req.ID = id.New("per")
	}
	s.pending[req.ID] = entry
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, req.ID)
		s.mu.Unlock()
	}()

	s.bus.Publish(session.Event{
		Type:      session.EventPermission,
		SessionID: req.SessionID,
		Data:      req,
	})

	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()

	select {
	case reply := <-entry.ch:
		return reply, nil
	case <-ctx.Done():
		return permission.Reply{}, ctx.Err()
	case <-timer.C:
		return permission.Reply{}, errors.New("permission request timed out")
	}
}

func (s *Server) handlePermissionReply(w http.ResponseWriter, r *http.Request) {
	reqID := r.PathValue("reqID")
	var input struct {
		Reply string `json:"reply"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}

	s.mu.Lock()
	entry, ok := s.pending[reqID]
	s.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("permission request not found"))
		return
	}

	reply := permission.Reply{}
	switch input.Reply {
	case "always":
		reply = permission.Reply{Allowed: true, Always: true}
	case "once", "allow":
		reply = permission.Reply{Allowed: true}
	case "reject", "deny":
		reply = permission.Reply{Allowed: false}
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unknown reply %q", input.Reply))
		return
	}

	select {
	case entry.ch <- reply:
	default:
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
