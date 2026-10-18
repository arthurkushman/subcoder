package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/arthurkushman/subcoder/internal/hook"
	"github.com/arthurkushman/subcoder/internal/id"
	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/rules"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/arthurkushman/subcoder/internal/skill"
	"github.com/arthurkushman/subcoder/internal/tool"
)

type Runner struct {
	Providers    *provider.Registry
	Tools        *tool.Registry
	Sessions     *session.Store
	Permissions  *permission.Engine
	Hooks        *hook.Engine
	Agents       []Agent
	Skills       []skill.Skill
	Instructions []string
	DefaultModel string
	MaxSteps     int
	Emit         session.Emitter

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func NewRunner(providers *provider.Registry, tools *tool.Registry, sessions *session.Store, permissions *permission.Engine) *Runner {
	return &Runner{
		Providers:   providers,
		Tools:       tools,
		Sessions:    sessions,
		Permissions: permissions,
		MaxSteps:    50,
		running:     map[string]context.CancelFunc{},
	}
}

type RunInput struct {
	Session *session.Session
	Agent   Agent
	Model   string
	Text    string
	Context string
}

func (r *Runner) emit(event session.Event) {
	if r.Emit != nil {
		r.Emit(event)
	}
}

func (r *Runner) steps() int {
	if r.MaxSteps > 0 {
		return r.MaxSteps
	}
	return 50
}

// Interrupt cancels a running session, if any.
func (r *Runner) Interrupt(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel, ok := r.running[sessionID]; ok {
		cancel()
		return true
	}
	return false
}

func (r *Runner) register(ctx context.Context, sessionID string) (context.Context, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, busy := r.running[sessionID]; busy {
		return nil, nil, fmt.Errorf("agent: session %s is already running", sessionID)
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.running[sessionID] = cancel
	return runCtx, func() {
		cancel()
		r.mu.Lock()
		delete(r.running, sessionID)
		r.mu.Unlock()
	}, nil
}

func (r *Runner) resolveModel(ctx context.Context, in RunInput) (provider.Provider, provider.Model, string, error) {
	modelID := in.Model
	if modelID == "" {
		modelID = in.Agent.Model
	}
	if modelID == "" {
		modelID = in.Session.Model
	}
	if modelID == "" {
		modelID = r.DefaultModel
	}
	pro, model, err := r.Providers.Resolve(ctx, modelID)
	if err != nil {
		return nil, provider.Model{}, "", err
	}
	return pro, model, model.FullID(), nil
}

// Run executes the agent loop until the model stops requesting tools.
func (r *Runner) Run(ctx context.Context, in RunInput) (*session.Message, error) {
	sess := in.Session
	if sess == nil {
		return nil, fmt.Errorf("agent: session is required")
	}

	runCtx, done, err := r.register(ctx, sess.ID)
	if err != nil {
		return nil, err
	}
	defer done()

	hookEvent := hook.Event{
		SessionID: sess.ID,
		Directory: sess.Directory,
		Agent:     in.Agent.Name,
	}
	hookEvent.Name = "session.start"
	if err := r.Hooks.Run(runCtx, hookEvent); err != nil {
		r.emit(session.Event{Type: session.EventSessionError, SessionID: sess.ID, Error: err.Error()})
		return nil, err
	}
	defer func() {
		endEvent := hookEvent
		endEvent.Name = "session.end"
		_ = r.Hooks.Run(context.Background(), endEvent)
	}()

	pro, model, fullID, err := r.resolveModel(runCtx, in)
	if err != nil {
		return nil, err
	}
	sess.Model = fullID
	if in.Agent.Name != "" {
		sess.Agent = in.Agent.Name
	}

	if in.Text != "" {
		msg := sess.AddUserMessage(in.Text)
		r.emit(session.Event{Type: session.EventMessageUpdated, SessionID: sess.ID, Message: msg})
	}
	if in.Context != "" {
		msg := sess.AddContextMessage(in.Context)
		r.emit(session.Event{Type: session.EventMessageUpdated, SessionID: sess.ID, Message: msg})
	}

	if err := r.Sessions.Save(sess); err != nil {
		return nil, err
	}

	tools := r.Tools.Subset(in.Agent.Tools)
	var last *session.Message

	for step := 0; step < r.steps(); step++ {
		if runCtx.Err() != nil {
			return last, runCtx.Err()
		}

		req := r.buildRequest(sess, in.Agent, pro, model)
		stream, err := pro.Stream(runCtx, req)
		if err != nil {
			assistant := sess.AddAssistantMessage(model.FullID(), pro.ID())
			assistant.Error = err.Error()
			r.emit(session.Event{Type: session.EventMessageUpdated, SessionID: sess.ID, Message: assistant.Clone()})
			_ = r.Sessions.Save(sess)
			return assistant, err
		}

		assistant := sess.AddAssistantMessage(model.FullID(), pro.ID())
		last = assistant
		r.emit(session.Event{Type: session.EventMessageUpdated, SessionID: sess.ID, Message: assistant.Clone()})

		var calls []provider.ToolCall
		streamErr := r.consumeStream(sess, assistant, model, stream, &calls)

		r.emit(session.Event{Type: session.EventMessageUpdated, SessionID: sess.ID, Message: assistant.Clone()})
		if err := r.Sessions.Save(sess); err != nil {
			return assistant, err
		}

		if streamErr != nil {
			assistant.Error = streamErr.Error()
			_ = r.Sessions.Save(sess)
			r.emit(session.Event{Type: session.EventSessionError, SessionID: sess.ID, Error: streamErr.Error()})
			return assistant, streamErr
		}

		if len(calls) == 0 {
			r.emit(session.Event{Type: session.EventSessionIdle, SessionID: sess.ID})
			return assistant, nil
		}

		for i := range calls {
			if runCtx.Err() != nil {
				r.emit(session.Event{Type: session.EventSessionIdle, SessionID: sess.ID})
				return assistant, runCtx.Err()
			}
			r.executeTool(runCtx, sess, assistant, in.Agent, tools, calls[i])
			if err := r.Sessions.Save(sess); err != nil {
				return assistant, err
			}
		}
	}

	r.emit(session.Event{Type: session.EventSessionIdle, SessionID: sess.ID})
	return last, fmt.Errorf("agent: reached maximum of %d steps", r.steps())
}

func (r *Runner) consumeStream(sess *session.Session, assistant *session.Message, model provider.Model, stream <-chan provider.Event, calls *[]provider.ToolCall) error {
	for event := range stream {
		switch event.Type {
		case provider.EventText:
			part := assistant.LastTextPart()
			if part == nil {
				part = assistant.AddPart(session.Part{Type: session.PartText})
			}
			part.Text += event.Text
			r.emit(session.Event{Type: session.EventPartUpdated, SessionID: sess.ID, Message: assistant.Clone(), Part: clonePtr(part)})
		case provider.EventReasoning:
			var part *session.Part
			for i := len(assistant.Parts) - 1; i >= 0; i-- {
				if assistant.Parts[i].Type == session.PartReasoning {
					part = &assistant.Parts[i]
					break
				}
			}
			if part == nil {
				part = assistant.AddPart(session.Part{Type: session.PartReasoning})
			}
			part.Text += event.Text
			r.emit(session.Event{Type: session.EventPartUpdated, SessionID: sess.ID, Message: assistant.Clone(), Part: clonePtr(part)})
		case provider.EventToolCall:
			*calls = append(*calls, *event.ToolCall)
			part := assistant.AddPart(session.Part{
				Type: session.PartTool,
				Tool: &session.ToolCall{
					ID:        event.ToolCall.ID,
					Name:      event.ToolCall.Name,
					Arguments: event.ToolCall.Arguments,
					State:     session.ToolPending,
				},
			})
			r.emit(session.Event{Type: session.EventPartUpdated, SessionID: sess.ID, Message: assistant.Clone(), Part: clonePtr(part)})
		case provider.EventUsage:
			usage := session.Usage{
				Input:      event.Usage.Input,
				Output:     event.Usage.Output,
				Reasoning:  event.Usage.Reasoning,
				CacheRead:  event.Usage.CacheRead,
				CacheWrite: event.Usage.CacheWrite,
			}
			usage.Cost = provider.Cost(model, *event.Usage)
			assistant.Usage.Add(usage)
			sess.Usage.Add(usage)
			sess.UpdatedAt = time.Now().UTC()
		case provider.EventError:
			return event.Err
		}
	}
	return nil
}

func (r *Runner) executeTool(ctx context.Context, sess *session.Session, assistant *session.Message, agentDef Agent, tools *tool.Registry, call provider.ToolCall) {
	part := findToolPart(assistant, call.ID)
	if part == nil {
		return
	}
	callPart := part.Tool
	callPart.State = session.ToolRunning
	callPart.Started = time.Now().UTC()
	r.emit(session.Event{Type: session.EventPartUpdated, SessionID: sess.ID, Message: assistant.Clone(), Part: clonePtr(part)})

	finish := func(state session.ToolState, title, output, errText string) {
		callPart.State = state
		callPart.Title = title
		callPart.Output = output
		callPart.Error = errText
		callPart.Ended = time.Now().UTC()
		r.emit(session.Event{Type: session.EventPartUpdated, SessionID: sess.ID, Message: assistant.Clone(), Part: clonePtr(part)})
	}

	selected, ok := tools.Get(call.Name)
	if !ok {
		finish(session.ToolError, call.Name, "", fmt.Sprintf("unknown tool %q", call.Name))
		return
	}

	pattern, title, details := permissionDetails(call.Name, call.Arguments)
	permReq := permission.Request{
		SessionID: sess.ID,
		Tool:      call.Name,
		Pattern:   pattern,
		Title:     title,
		Details:   details,
	}
	if r.Permissions != nil {
		if err := r.Permissions.Check(ctx, permReq); err != nil {
			finish(session.ToolError, title, "", err.Error())
			return
		}
	}

	env := &tool.Context{
		Directory: sess.Directory,
		SessionID: sess.ID,
		Agent:     agentDef.Name,
		Todos:     &todoAdapter{sess: sess},
	}

	hookEvent := hook.Event{
		SessionID: sess.ID,
		Directory: sess.Directory,
		Agent:     agentDef.Name,
		Tool:      call.Name,
		Arguments: call.Arguments,
	}
	hookEvent.Name = "tool.before"
	if err := r.Hooks.Run(ctx, hookEvent); err != nil {
		finish(session.ToolError, title, "", "blocked by hook: "+err.Error())
		return
	}

	result, err := selected.Execute(ctx, json.RawMessage(call.Arguments), env)
	if err != nil {
		finish(session.ToolError, title, "", err.Error())
		return
	}
	callPart.Metadata = result.Metadata
	finish(session.ToolCompleted, result.Title, result.Output, "")

	hookEvent.Name = "tool.after"
	hookEvent.Result = result.Output
	_ = r.Hooks.Run(ctx, hookEvent)
}

func findToolPart(msg *session.Message, callID string) *session.Part {
	for i := range msg.Parts {
		if msg.Parts[i].Type == session.PartTool && msg.Parts[i].Tool != nil && msg.Parts[i].Tool.ID == callID {
			return &msg.Parts[i]
		}
	}
	return nil
}

func permissionDetails(toolName, args string) (pattern, title, details string) {
	var parsed map[string]any
	_ = json.Unmarshal([]byte(args), &parsed)

	get := func(key string) string {
		if value, ok := parsed[key].(string); ok {
			return value
		}
		return ""
	}

	switch toolName {
	case "bash":
		pattern = get("command")
	case "read", "write", "edit":
		pattern = get("filePath")
	case "glob":
		pattern = get("pattern")
	case "grep":
		pattern = get("pattern")
	case "ls":
		pattern = get("path")
	case "webfetch":
		pattern = get("url")
	case "task":
		pattern = get("subagent_type")
	default:
		pattern = ""
	}

	title = toolName
	if pattern != "" {
		title = fmt.Sprintf("%s: %s", toolName, truncate(pattern, 60))
	}
	details = args
	return pattern, title, details
}

func clonePtr(part *session.Part) *session.Part {
	if part == nil {
		return nil
	}
	clone := part.Clone()
	return &clone
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func (r *Runner) buildRequest(sess *session.Session, agentDef Agent, pro provider.Provider, model provider.Model) provider.Request {
	req := provider.Request{
		Model:       model.ID,
		System:      r.SystemPrompt(sess, agentDef),
		Messages:    ToChatMessages(sess.Messages),
		Temperature: agentDef.Temperature,
	}
	tools := r.Tools.Subset(agentDef.Tools)
	for _, spec := range tools.Specs(nil) {
		req.Tools = append(req.Tools, provider.ToolSpec{Name: spec.Name, Description: spec.Description, Parameters: spec.Parameters})
	}
	return req
}

// ToChatMessages converts session history into provider wire messages.
func ToChatMessages(messages []session.Message) []provider.ChatMessage {
	var out []provider.ChatMessage
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			out = append(out, provider.ChatMessage{Role: "user", Text: msg.Text()})
		case "assistant":
			calls := msg.ToolCalls()
			text := msg.Text()
			if text != "" || len(calls) > 0 {
				wire := provider.ChatMessage{Role: "assistant", Text: text}
				for _, call := range calls {
					wire.ToolCalls = append(wire.ToolCalls, provider.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments})
				}
				out = append(out, wire)
			}
			for _, call := range calls {
				out = append(out, provider.ChatMessage{
					Role:       "tool",
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Text:       toolResultText(call),
				})
			}
		}
	}
	return out
}

func toolResultText(call *session.ToolCall) string {
	if call.State == session.ToolError {
		if call.Error != "" {
			return "Error: " + call.Error
		}
		return "Error: tool failed"
	}
	if call.Output == "" {
		return "(no output)"
	}
	return call.Output
}

func (r *Runner) SystemPrompt(sess *session.Session, agentDef Agent) string {
	var sb strings.Builder

	prompt := agentDef.Prompt
	if prompt == "" {
		prompt = basePrompt
	}
	sb.WriteString(prompt)
	sb.WriteString("\n\n")

	fmt.Fprintf(&sb, "Environment:\n- Working directory: %s\n- Platform: %s\n- Today's date: %s\n", sess.Directory, runtime.GOOS, time.Now().Format("2006-01-02"))
	if sess.Model != "" {
		fmt.Fprintf(&sb, "- Model: %s\n", sess.Model)
	}
	if agentDef.Name != "" {
		fmt.Fprintf(&sb, "- Agent: %s\n", agentDef.Name)
	}

	if sess.Todos != nil {
		var lines []string
		for _, item := range sess.Todos {
			lines = append(lines, fmt.Sprintf("- [%s] %s", item.Status, item.Content))
		}
		if len(lines) > 0 {
			sb.WriteString("\nCurrent todo list:\n" + strings.Join(lines, "\n") + "\n")
		}
	}

	rulesText := rules.Combine(rules.Discover(sess.Directory))
	if rulesText != "" {
		sb.WriteString("\nProject instructions:\n" + rulesText + "\n")
	}

	if len(r.Instructions) > 0 {
		sb.WriteString("\nAdditional instructions:\n- " + strings.Join(r.Instructions, "\n- ") + "\n")
	}

	if len(r.Skills) > 0 {
		sb.WriteString("\nAvailable skills (use the skill tool with the skill name to load full instructions):\n")
		for _, loaded := range r.Skills {
			fmt.Fprintf(&sb, "- %s: %s\n", loaded.Name, loaded.Description)
		}
	}

	return strings.TrimSpace(sb.String())
}

type todoAdapter struct {
	sess *session.Session
}

func (a *todoAdapter) SetTodos(items []tool.TodoItem) {
	todos := make([]session.Todo, 0, len(items))
	for _, item := range items {
		todos = append(todos, session.Todo{Content: item.Content, Status: item.Status, Priority: item.Priority})
	}
	a.sess.SetTodos(todos)
}

func (a *todoAdapter) Todos() []tool.TodoItem {
	items := make([]tool.TodoItem, 0, len(a.sess.Todos))
	for _, todo := range a.sess.Todos {
		items = append(items, tool.TodoItem{Content: todo.Content, Status: todo.Status, Priority: todo.Priority})
	}
	return items
}

var _ tool.TaskRunner = (*Runner)(nil)

// RunTask implements tool.TaskRunner by running a subagent session to
// completion and returning its final text.
func (r *Runner) RunTask(ctx context.Context, req tool.TaskRequest) (tool.TaskResult, error) {
	child := session.New(req.Directory)
	child.ParentID = req.ParentSessionID
	child.Agent = req.Agent
	if err := r.Sessions.Save(child); err != nil {
		return tool.TaskResult{}, err
	}

	agentDef, ok := Find(r.Agents, req.Agent)
	if !ok {
		agentDef, _ = Find(r.Agents, "general")
	}
	if !agentDef.IsSubagent() {
		agentDef.Mode = ModeSubagent
	}

	message, err := r.Run(ctx, RunInput{Session: child, Agent: agentDef, Text: req.Prompt})
	if err != nil {
		return tool.TaskResult{}, err
	}

	text := ""
	if message != nil {
		text = message.Text()
	}
	return tool.TaskResult{SessionID: child.ID, Text: text}, nil
}

// Compact summarizes the session history and replaces it with the summary
// followed by the trailing turn.
func (r *Runner) Compact(ctx context.Context, sess *session.Session, model string) (*session.Message, error) {
	if len(sess.Messages) == 0 {
		return nil, fmt.Errorf("agent: nothing to compact")
	}

	pro, resolved, _, err := r.resolveModel(ctx, RunInput{Session: sess, Model: model})
	if err != nil {
		return nil, err
	}

	messages := ToChatMessages(sess.Messages)
	messages = append(messages, provider.ChatMessage{
		Role: "user",
		Text: "Summarize the conversation above into a compact briefing that preserves: the user's goals, decisions made, files changed, current state, and any next steps. Be concise but complete.",
	})

	stream, err := pro.Stream(ctx, provider.Request{
		Model:    resolved.ID,
		System:   "You are a precise conversation summarizer. Output only the summary.",
		Messages: messages,
	})
	if err != nil {
		return nil, err
	}

	var summary strings.Builder
	for event := range stream {
		switch event.Type {
		case provider.EventText:
			summary.WriteString(event.Text)
		case provider.EventError:
			return nil, event.Err
		}
	}

	if strings.TrimSpace(summary.String()) == "" {
		return nil, fmt.Errorf("agent: summarizer returned no text")
	}

	lastUser := -1
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == "user" {
			lastUser = i
			break
		}
	}

	var kept []session.Message
	if lastUser >= 0 {
		kept = append(kept, sess.Messages[lastUser])
	}

	return r.replaceWithSummary(sess, summary.String(), resolved.FullID(), pro.ID(), kept)
}

func (r *Runner) replaceWithSummary(sess *session.Session, summary, model, providerID string, kept []session.Message) (*session.Message, error) {
	msg := session.Message{
		ID:       id.New("msg"),
		Role:     "assistant",
		Summary:  true,
		Model:    model,
		Provider: providerID,
		Time:     time.Now().UTC(),
	}
	msg.AddPart(session.Part{Type: session.PartSummary, Text: summary})

	sess.Messages = append([]session.Message{msg}, kept...)
	if len(kept) == 0 {
		sess.Title = session.DeriveTitle(summary)
	}
	sess.UpdatedAt = time.Now().UTC()

	if err := r.Sessions.Save(sess); err != nil {
		return nil, err
	}
	r.emit(session.Event{Type: session.EventSessionUpdated, SessionID: sess.ID})
	return &sess.Messages[0], nil
}
