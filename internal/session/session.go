package session

import (
	"sort"
	"strings"
	"time"

	"github.com/arthurkushman/subcoder/internal/id"
)

type PartType string

const (
	PartText      PartType = "text"
	PartReasoning PartType = "reasoning"
	PartTool      PartType = "tool"
	PartSummary   PartType = "summary"
)

type ToolState string

const (
	ToolPending   ToolState = "pending"
	ToolRunning   ToolState = "running"
	ToolCompleted ToolState = "completed"
	ToolError     ToolState = "error"
)

type Usage struct {
	Input      float64 `json:"input,omitempty"`
	Output     float64 `json:"output,omitempty"`
	Reasoning  float64 `json:"reasoning,omitempty"`
	CacheRead  float64 `json:"cacheRead,omitempty"`
	CacheWrite float64 `json:"cacheWrite,omitempty"`
	Cost       float64 `json:"cost,omitempty"`
}

func (u *Usage) Add(other Usage) {
	u.Input += other.Input
	u.Output += other.Output
	u.Reasoning += other.Reasoning
	u.CacheRead += other.CacheRead
	u.CacheWrite += other.CacheWrite
	u.Cost += other.Cost
}

type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments string         `json:"arguments"`
	State     ToolState      `json:"state"`
	Title     string         `json:"title,omitempty"`
	Output    string         `json:"output,omitempty"`
	Error     string         `json:"error,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Started   time.Time      `json:"started,omitempty"`
	Ended     time.Time      `json:"ended,omitempty"`
}

type Part struct {
	ID   string    `json:"id"`
	Type PartType  `json:"type"`
	Text string    `json:"text,omitempty"`
	Tool *ToolCall `json:"tool,omitempty"`
	Time time.Time `json:"time"`
}

type Message struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Parts     []Part    `json:"parts"`
	Model     string    `json:"model,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Usage     Usage     `json:"usage,omitempty"`
	Error     string    `json:"error,omitempty"`
	Summary   bool      `json:"summary,omitempty"`
	Synthetic bool      `json:"synthetic,omitempty"`
	Snapshot  string    `json:"snapshot,omitempty"`
	Time      time.Time `json:"time"`
}

func (m *Message) Text() string {
	var sb strings.Builder
	for _, part := range m.Parts {
		if part.Type == PartText || part.Type == PartSummary {
			sb.WriteString(part.Text)
		}
	}
	return sb.String()
}

func (m *Message) ToolCalls() []*ToolCall {
	var calls []*ToolCall
	for i := range m.Parts {
		if m.Parts[i].Type == PartTool && m.Parts[i].Tool != nil {
			calls = append(calls, m.Parts[i].Tool)
		}
	}
	return calls
}

func (p Part) Clone() Part {
	clone := p
	if p.Tool != nil {
		tool := *p.Tool
		clone.Tool = &tool
	}
	return clone
}

// Clone returns a deep copy of the message, safe to hand to other goroutines.
func (m *Message) Clone() *Message {
	clone := *m
	clone.Parts = make([]Part, len(m.Parts))
	for i, part := range m.Parts {
		clone.Parts[i] = part.Clone()
	}
	return &clone
}

func (m *Message) AddPart(part Part) *Part {
	if part.ID == "" {
		part.ID = id.New("prt")
	}
	if part.Time.IsZero() {
		part.Time = time.Now().UTC()
	}
	m.Parts = append(m.Parts, part)
	return &m.Parts[len(m.Parts)-1]
}

func (m *Message) LastTextPart() *Part {
	for i := len(m.Parts) - 1; i >= 0; i-- {
		if m.Parts[i].Type == PartText {
			return &m.Parts[i]
		}
	}
	return nil
}

type Todo struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	Directory string    `json:"directory,omitempty"`
	ParentID  string    `json:"parentID,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Model     string    `json:"model,omitempty"`
	Messages  []Message `json:"messages"`
	Todos     []Todo    `json:"todos,omitempty"`
	Usage     Usage     `json:"usage,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func New(directory string) *Session {
	now := time.Now().UTC()
	return &Session{
		ID:        id.New("ses"),
		Directory: directory,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (s *Session) AddUserMessage(text string) *Message {
	msg := Message{
		ID:   id.New("msg"),
		Role: "user",
		Time: time.Now().UTC(),
	}
	if text != "" {
		msg.AddPart(Part{Type: PartText, Text: text})
	}
	s.Messages = append(s.Messages, msg)
	if s.Title == "" {
		s.Title = DeriveTitle(text)
	}
	s.touch()
	return &s.Messages[len(s.Messages)-1]
}

// AddContextMessage appends a user message that provides attached context to
// the model without being shown as a user turn in the transcript.
func (s *Session) AddContextMessage(text string) *Message {
	msg := Message{
		ID:        id.New("msg"),
		Role:      "user",
		Synthetic: true,
		Time:      time.Now().UTC(),
	}
	msg.AddPart(Part{Type: PartText, Text: text})
	s.Messages = append(s.Messages, msg)
	s.touch()
	return &s.Messages[len(s.Messages)-1]
}

func (s *Session) AddAssistantMessage(model, provider string) *Message {
	msg := Message{
		ID:       id.New("msg"),
		Role:     "assistant",
		Model:    model,
		Provider: provider,
		Time:     time.Now().UTC(),
	}
	s.Messages = append(s.Messages, msg)
	s.touch()
	return &s.Messages[len(s.Messages)-1]
}

func (s *Session) Message(id string) *Message {
	for i := range s.Messages {
		if s.Messages[i].ID == id {
			return &s.Messages[i]
		}
	}
	return nil
}

func (s *Session) LastAssistantMessage() *Message {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role == "assistant" {
			return &s.Messages[i]
		}
	}
	return nil
}

func (s *Session) SetTodos(todos []Todo) {
	s.Todos = todos
	s.touch()
}

func (s *Session) touch() {
	s.UpdatedAt = time.Now().UTC()
}

// Undo removes everything after the last user message, returning the text of
// the removed user message. It reports false when there is nothing to undo.
func (s *Session) Undo() (string, bool) {
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role == "user" {
			text := s.Messages[i].Text()
			s.Messages = s.Messages[:i]
			if s.Title == DeriveTitle(text) {
				s.Title = ""
				for _, msg := range s.Messages {
					if msg.Role == "user" {
						s.Title = DeriveTitle(msg.Text())
						break
					}
				}
			}
			s.touch()
			return text, true
		}
	}
	return "", false
}

func DeriveTitle(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > 60 {
		return strings.TrimSpace(string(runes[:60])) + "..."
	}
	return text
}

func SortByUpdated(sessions []*Session) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
}

type EventType string

const (
	EventSessionUpdated EventType = "session.updated"
	EventMessageUpdated EventType = "message.updated"
	EventPartUpdated    EventType = "part.updated"
	EventSessionIdle    EventType = "session.idle"
	EventSessionError   EventType = "session.error"
	EventPermission     EventType = "permission.asked"
)

type Event struct {
	Type      EventType `json:"type"`
	SessionID string    `json:"sessionID"`
	Message   *Message  `json:"message,omitempty"`
	Part      *Part     `json:"part,omitempty"`
	Error     string    `json:"error,omitempty"`
	Data      any       `json:"data,omitempty"`
}

type Emitter func(Event)
