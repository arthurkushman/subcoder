// Package acp implements a subset of the Agent Client Protocol (ACP): a
// JSON-RPC 2.0 protocol over stdio that lets editors drive the agent.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/arthurkushman/subcoder/internal/agent"
	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/session"
)

const ProtocolVersion = 1

type Options struct {
	Runner     *agent.Runner
	Sessions   *session.Store
	Agents     []agent.Agent
	Permission *permission.Engine
	Directory  string
	Model      string
	In         io.Reader
	Out        io.Writer
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type Server struct {
	opts Options

	writeMu sync.Mutex
	enc     *json.Encoder

	mu       sync.Mutex
	sessions map[string]*session.Session
	cancels  map[string]context.CancelFunc
	pending  map[string]chan permission.Reply
	nextID   int
	chunks   map[string]int
	wg       sync.WaitGroup
}

// Serve processes ACP JSON-RPC messages from opts.In until EOF or ctx is done.
func Serve(ctx context.Context, opts Options) error {
	if opts.Runner == nil {
		return errors.New("acp: runner is required")
	}
	if opts.In == nil {
		opts.In = strings.NewReader("")
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}

	s := &Server{
		opts:     opts,
		enc:      json.NewEncoder(opts.Out),
		sessions: map[string]*session.Session{},
		cancels:  map[string]context.CancelFunc{},
		pending:  map[string]chan permission.Reply{},
		chunks:   map[string]int{},
	}
	opts.Runner.Emit = s.emit
	if opts.Permission != nil {
		opts.Permission.SetAsker(s)
	}

	scanner := bufio.NewScanner(opts.In)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		s.handleLine(ctx, []byte(line))
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	s.wg.Wait()
	return nil
}

func (s *Server) write(message rpcMessage) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.enc.Encode(message)
}

func (s *Server) respond(id json.RawMessage, result any) {
	s.write(rpcMessage{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) respondError(id json.RawMessage, code int, message string) {
	s.write(rpcMessage{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (s *Server) notify(method string, params any) {
	s.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: mustRaw(params)})
}

func mustRaw(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func (s *Server) handleLine(ctx context.Context, line []byte) {
	var message rpcMessage
	if err := json.Unmarshal(line, &message); err != nil {
		s.respondError(nil, -32700, "parse error: "+err.Error())
		return
	}

	if message.Method == "" {
		s.handleResponse(message)
		return
	}

	switch message.Method {
	case "initialize":
		s.respond(message.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"agentCapabilities": map[string]any{
				"loadSession": false,
				"promptCapabilities": map[string]any{
					"image":           false,
					"audio":           false,
					"embeddedContext": true,
				},
			},
			"authMethods": []any{},
		})
	case "authenticate":
		s.respond(message.ID, map[string]any{})
	case "session/new":
		s.handleNewSession(message)
	case "session/prompt":
		s.handlePrompt(ctx, message)
	case "session/cancel":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(message.Params, &params)
		s.cancel(params.SessionID)
	default:
		if len(message.ID) > 0 {
			s.respondError(message.ID, -32601, "method not found: "+message.Method)
		}
	}
}

func (s *Server) handleNewSession(message rpcMessage) {
	var params struct {
		Cwd       string `json:"cwd"`
		Model     string `json:"model"`
		Agent     string `json:"agent"`
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(message.Params, &params)

	directory := params.Cwd
	if directory == "" {
		directory = s.opts.Directory
	}
	sess := session.New(directory)
	sess.Model = params.Model
	sess.Agent = params.Agent

	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()

	if s.opts.Sessions != nil {
		if err := s.opts.Sessions.Save(sess); err != nil {
			s.respondError(message.ID, -32603, err.Error())
			return
		}
	}
	s.respond(message.ID, map[string]any{"sessionId": sess.ID})
}

func (s *Server) handlePrompt(ctx context.Context, message rpcMessage) {
	var params struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil {
		s.respondError(message.ID, -32602, "invalid params: "+err.Error())
		return
	}

	s.mu.Lock()
	sess := s.sessions[params.SessionID]
	s.mu.Unlock()
	if sess == nil {
		s.respondError(message.ID, -32602, "unknown session: "+params.SessionID)
		return
	}

	var text strings.Builder
	for _, block := range params.Prompt {
		if block.Type == "" || block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		s.respondError(message.ID, -32602, "prompt is empty")
		return
	}

	promptCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancels[sess.ID] = cancel
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() {
			s.mu.Lock()
			delete(s.cancels, sess.ID)
			s.mu.Unlock()
		}()

		agentDef := agent.Default(s.opts.Agents)
		_, err := s.opts.Runner.Run(promptCtx, agent.RunInput{
			Session: sess,
			Agent:   agentDef,
			Model:   s.opts.Model,
			Text:    text.String(),
		})
		switch {
		case err == nil:
			s.respond(message.ID, map[string]any{"stopReason": "end_turn"})
		case errors.Is(err, context.Canceled):
			s.respond(message.ID, map[string]any{"stopReason": "cancelled"})
		default:
			s.respondError(message.ID, -32603, err.Error())
		}
	}()
}

func (s *Server) cancel(sessionID string) {
	s.mu.Lock()
	cancel := s.cancels[sessionID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) emit(event session.Event) {
	switch event.Type {
	case session.EventPartUpdated:
		if event.Message == nil || event.Part == nil {
			return
		}
		part := event.Part
		switch part.Type {
		case session.PartText:
			s.sendChunk(event, part.ID, part.Text, "agent_message_chunk")
		case session.PartReasoning:
			s.sendChunk(event, part.ID, part.Text, "agent_thought_chunk")
		case session.PartTool:
			if part.Tool == nil {
				return
			}
			s.notify("session/update", map[string]any{
				"sessionId": event.SessionID,
				"update": map[string]any{
					"sessionUpdate": "tool_call_update",
					"toolCallId":    part.Tool.ID,
					"title":         part.Tool.Title,
					"kind":          "other",
					"status":        toolStatus(part.Tool.State),
					"content": []any{map[string]any{
						"type": "content",
						"content": map[string]any{
							"type": "text",
							"text": toolText(part.Tool),
						},
					}},
				},
			})
		}
	}
}

func (s *Server) sendChunk(event session.Event, partID, text, kind string) {
	s.mu.Lock()
	sent := s.chunks[partID]
	if sent > len(text) {
		sent = 0
	}
	delta := text[sent:]
	s.chunks[partID] = len(text)
	s.mu.Unlock()
	if delta == "" {
		return
	}
	s.notify("session/update", map[string]any{
		"sessionId": event.SessionID,
		"update": map[string]any{
			"sessionUpdate": kind,
			"content":       map[string]any{"type": "text", "text": delta},
		},
	})
}

func toolStatus(state session.ToolState) string {
	switch state {
	case session.ToolPending:
		return "pending"
	case session.ToolRunning:
		return "in_progress"
	case session.ToolCompleted:
		return "completed"
	case session.ToolError:
		return "failed"
	default:
		return "pending"
	}
}

func toolText(call *session.ToolCall) string {
	if call.State == session.ToolError {
		return call.Error
	}
	return call.Output
}

// Ask implements permission.Asker by sending session/request_permission to
// the client and waiting for its selected option.
func (s *Server) Ask(ctx context.Context, req permission.Request) (permission.Reply, error) {
	if req.ID == "" {
		s.mu.Lock()
		s.nextID++
		req.ID = "per_" + strconv.Itoa(s.nextID)
		s.mu.Unlock()
	}

	reply := make(chan permission.Reply, 1)
	s.mu.Lock()
	s.pending[req.ID] = reply
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, req.ID)
		s.mu.Unlock()
	}()

	params := map[string]any{
		"sessionId": req.SessionID,
		"toolCall": map[string]any{
			"toolCallId": req.ID,
			"title":      req.Title,
			"kind":       "other",
			"status":     "pending",
			"rawInput":   req.Details,
		},
		"options": []any{
			map[string]any{"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
			map[string]any{"optionId": "allow-always", "name": "Always allow", "kind": "allow_always"},
			map[string]any{"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
		},
	}
	s.write(rpcMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage(strconv.Quote("perm_" + req.ID)),
		Method:  "session/request_permission",
		Params:  mustRaw(params),
	})

	select {
	case result := <-reply:
		return result, nil
	case <-ctx.Done():
		return permission.Reply{}, ctx.Err()
	}
}

func (s *Server) handleResponse(message rpcMessage) {
	var id string
	if err := json.Unmarshal(message.ID, &id); err != nil {
		return
	}
	if !strings.HasPrefix(id, "perm_") {
		return
	}
	requestID := strings.TrimPrefix(id, "perm_")

	s.mu.Lock()
	reply := s.pending[requestID]
	s.mu.Unlock()
	if reply == nil {
		return
	}

	if message.Error != nil {
		reply <- permission.Reply{Allowed: false}
		return
	}
	var result struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	raw, _ := json.Marshal(message.Result)
	_ = json.Unmarshal(raw, &result)
	if result.Outcome.Outcome != "selected" {
		reply <- permission.Reply{Allowed: false}
		return
	}
	switch result.Outcome.OptionID {
	case "allow-always":
		reply <- permission.Reply{Allowed: true, Always: true}
	case "allow-once", "allow":
		reply <- permission.Reply{Allowed: true}
	default:
		reply <- permission.Reply{Allowed: false}
	}
}

var _ permission.Asker = (*Server)(nil)
