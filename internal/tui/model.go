package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/arthurkushman/subcoder/internal/agent"
	"github.com/arthurkushman/subcoder/internal/command"
	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/prompt"
	"github.com/arthurkushman/subcoder/internal/provider"
	"github.com/arthurkushman/subcoder/internal/rules"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/arthurkushman/subcoder/internal/snapshot"
	"github.com/arthurkushman/subcoder/internal/version"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type Options struct {
	Config      *config.Config
	Providers   *provider.Registry
	Sessions    *session.Store
	Runner      *agent.Runner
	Agents      []agent.Agent
	Permission  *permission.Engine
	Snapshots   *snapshot.Manager
	Directory   string
	Model       string
	Agent       string
	Prompt      string
	SessionID   string
	Continue    bool
	Fork        bool
	AutoApprove bool
}

type eventMsg session.Event

type runFinished struct {
	err error
}

type modelsLoaded struct {
	models []provider.Model
}

type shellDone struct {
	command string
	output  string
	err     error
}

type compactDone struct {
	err error
}

type editorDone struct {
	text string
	err  error
}

type pickerKind int

const (
	pickerModels pickerKind = iota
	pickerSessions
	pickerAgents
	pickerThemes
)

type pickerItem struct {
	id          string
	label       string
	description string
}

type picker struct {
	kind   pickerKind
	title  string
	items  []pickerItem
	cursor int
}

type model struct {
	opts    Options
	styles  theme
	spinner spinner.Model
	input   textarea.Model
	view    viewport.Model

	width, height int
	ready         bool

	session   *session.Session
	messages  []session.Message
	title     string
	modelID   string
	agentName string

	streaming      bool
	status         string
	notices        []string
	details        bool
	showThinking   bool
	showTimestamps bool
	themeName      string
	commands       []command.Command

	pendingSnapshot *snapshot.Snapshot

	events    chan tea.Msg
	cancelRun context.CancelFunc

	permReq   *permission.Request
	permReply chan permission.Reply

	picker *picker

	undone []undoState
}

type undoState struct {
	Messages []session.Message
	Files    string
}

func newModel(opts Options) *model {
	if opts.Runner != nil {
		events := make(chan tea.Msg, 1024)

		m := &model{
			opts:    opts,
			styles:  newTheme(opts.Config.Theme),
			spinner: spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipglossDim())),
			events:  events,
		}

		opts.Runner.Emit = func(event session.Event) {
			select {
			case events <- eventMsg(event):
			default:
			}
		}
		return m
	}

	return &model{opts: opts, styles: newTheme(""), events: make(chan tea.Msg, 1024)}
}

func (m *model) initialize() {
	m.showThinking = true
	m.themeName = m.opts.Config.Theme
	if m.themeName == "" {
		m.themeName = "default"
	}
	m.commands = command.Load(m.opts.Directory)
	m.styles = newTheme(m.themeName)

	input := textarea.New()
	input.Placeholder = "Ask SubCoder... (/help for commands, @file for context, !cmd for shell)"
	input.ShowLineNumbers = false
	input.CharLimit = 0
	input.SetHeight(3)
	input.Prompt = ""
	m.input = input
	m.input.Focus()

	view := viewport.New(80, 20)
	m.view = view

	sess := m.loadInitialSession()
	m.session = sess
	m.syncFromSession()

	m.agentName = sess.Agent
	if m.agentName == "" {
		if m.opts.Agent != "" {
			m.agentName = m.opts.Agent
		} else {
			m.agentName = agent.Default(m.opts.Agents).Name
		}
	}

	m.modelID = sess.Model
	if m.modelID == "" {
		m.modelID = m.opts.Model
	}

	m.notice(fmt.Sprintf("SubCoder %s · model %s · agent %s · /help for commands", version.Version, m.modelID, m.agentName))
}

func (m *model) loadInitialSession() *session.Session {
	var sess *session.Session
	var err error

	switch {
	case m.opts.SessionID != "":
		sess, err = m.opts.Sessions.Load(m.opts.SessionID)
	case m.opts.Continue:
		sess, err = m.opts.Sessions.Latest(m.opts.Directory)
	}
	if err != nil || sess == nil {
		return session.New(m.opts.Directory)
	}
	if m.opts.Fork {
		forked := session.New(m.opts.Directory)
		forked.Title = sess.Title
		forked.Model = sess.Model
		forked.Agent = sess.Agent
		forked.Messages = cloneMessages(sess.Messages)
		forked.Todos = append([]session.Todo(nil), sess.Todos...)
		forked.ParentID = sess.ID
		return forked
	}
	return sess
}

func (m *model) syncFromSession() {
	m.messages = cloneMessages(m.session.Messages)
	m.title = m.session.Title
}

func cloneMessages(messages []session.Message) []session.Message {
	out := make([]session.Message, len(messages))
	for i := range messages {
		out[i] = *messages[i].Clone()
	}
	return out
}

func (m *model) notice(text string) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	m.notices = append(m.notices, lines...)
	if len(m.notices) > 200 {
		m.notices = m.notices[len(m.notices)-200:]
	}
	m.rebuild()
}

func (m *model) applyEvent(event session.Event) {
	if m.session == nil || event.SessionID != m.session.ID {
		return
	}
	if event.Message == nil {
		return
	}
	msg := event.Message
	found := false
	for i := range m.messages {
		if m.messages[i].ID == msg.ID {
			m.messages[i] = *msg
			found = true
			break
		}
	}
	if !found {
		m.messages = append(m.messages, *msg)
	}
	if msg.Role == "user" && m.title == "" {
		m.title = session.DeriveTitle(msg.Text())
	}
	m.rebuild()
}

func (m *model) currentAgent() agent.Agent {
	if found, ok := agent.Find(m.opts.Agents, m.agentName); ok {
		return found
	}
	return agent.Default(m.opts.Agents)
}

func (m *model) cycleAgent() {
	var primaries []agent.Agent
	for _, a := range m.opts.Agents {
		if a.IsPrimary() {
			primaries = append(primaries, a)
		}
	}
	if len(primaries) == 0 {
		return
	}
	for i, a := range primaries {
		if a.Name == m.agentName {
			m.agentName = primaries[(i+1)%len(primaries)].Name
			m.session.Agent = m.agentName
			_ = m.opts.Sessions.Save(m.session)
			m.notice("agent: " + m.agentName)
			return
		}
	}
	m.agentName = primaries[0].Name
}

func (m *model) submit(input string) tea.Cmd {
	text := strings.TrimSpace(input)
	if text == "" {
		return nil
	}

	if cmd, ok := parseCommand(text); ok {
		return m.handleCommand(cmd)
	}
	if strings.HasPrefix(text, "!") {
		return m.runShell(strings.TrimSpace(text[1:]))
	}
	return m.startRun(text)
}

func (m *model) startRun(text string) tea.Cmd {
	if m.streaming {
		m.notice("already streaming, press esc to interrupt")
		return nil
	}

	attachments, files := prompt.FileContext(text, m.opts.Directory)
	if len(files) > 0 {
		m.notice("attached: " + strings.Join(files, ", "))
	}

	m.pendingSnapshot = nil
	if m.opts.Snapshots != nil {
		snapCtx, cancelSnap := context.WithTimeout(context.Background(), 60*time.Second)
		snap, err := m.opts.Snapshots.Create(snapCtx)
		cancelSnap()
		if err != nil {
			m.notice("snapshot: " + err.Error())
		} else {
			m.pendingSnapshot = snap
		}
	}

	m.streaming = true
	m.status = "thinking..."
	m.rebuild()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelRun = cancel

	runSession := m.session
	agentDef := m.currentAgent()
	modelID := m.modelID
	runner := m.opts.Runner

	go func() {
		_, err := runner.Run(ctx, agent.RunInput{Session: runSession, Agent: agentDef, Model: modelID, Text: text, Context: attachments})
		select {
		case m.events <- runFinished{err: err}:
		default:
		}
	}()

	return m.spinner.Tick
}

func (m *model) handleCommand(cmd slashCommand) tea.Cmd {
	switch cmd.Name {
	case "help":
		m.notice(helpText(m.commands))
	case "init":
		path := filepath.Join(m.opts.Directory, rules.FileName)
		if _, err := os.Stat(path); err == nil {
			m.notice(path + " already exists")
			break
		}
		if err := os.WriteFile(path, []byte(rules.Init(m.opts.Directory)), 0o644); err != nil {
			m.notice("error: " + err.Error())
			break
		}
		m.notice("created " + path)
	case "new":
		_ = m.opts.Sessions.Save(m.session)
		m.session = session.New(m.opts.Directory)
		m.undone = nil
		m.pendingSnapshot = nil
		m.syncFromSession()
		m.notice("new session " + m.session.ID)
	case "sessions":
		summaries, err := m.opts.Sessions.List()
		if err != nil {
			m.notice("error: " + err.Error())
			break
		}
		items := make([]pickerItem, 0, len(summaries))
		for _, summary := range summaries {
			title := summary.Title
			if title == "" {
				title = "(untitled)"
			}
			items = append(items, pickerItem{id: summary.ID, label: title, description: summary.UpdatedAt + "  " + summary.Model})
		}
		m.picker = &picker{kind: pickerSessions, title: "Sessions", items: items}
	case "models":
		return loadModelsCmd(m.opts.Providers)
	case "agents":
		var items []pickerItem
		for _, a := range m.opts.Agents {
			items = append(items, pickerItem{id: a.Name, label: a.Name, description: a.Description})
		}
		m.picker = &picker{kind: pickerAgents, title: "Agents", items: items}
	case "themes":
		var items []pickerItem
		for _, name := range themeNames() {
			items = append(items, pickerItem{id: name, label: name, description: "color theme"})
		}
		m.picker = &picker{kind: pickerThemes, title: "Themes", items: items}
	case "commands":
		if len(m.commands) == 0 {
			m.notice("no custom commands; add markdown files in .subcoder/command/")
			break
		}
		var sb strings.Builder
		sb.WriteString("Custom commands:\n")
		for _, custom := range m.commands {
			fmt.Fprintf(&sb, "  /%-12s %s\n", custom.Name, custom.Description)
		}
		m.notice(sb.String())
	case "details":
		m.details = !m.details
		m.notice(fmt.Sprintf("tool details: %v", m.details))
	case "thinking":
		m.showThinking = !m.showThinking
		m.notice(fmt.Sprintf("reasoning blocks: %v", m.showThinking))
	case "timestamps":
		m.showTimestamps = !m.showTimestamps
		m.notice(fmt.Sprintf("timestamps: %v", m.showTimestamps))
	case "editor":
		return m.openEditor(strings.Join(cmd.Args, " "))
	case "undo":
		if m.streaming {
			m.notice("cannot undo while streaming")
			break
		}
		restoreID := ""
		for i := len(m.session.Messages) - 1; i >= 0; i-- {
			if m.session.Messages[i].Role == "user" && !m.session.Messages[i].Synthetic {
				restoreID = m.session.Messages[i].Snapshot
				break
			}
		}
		before := cloneMessages(m.session.Messages)
		afterID := ""
		if restoreID != "" && m.opts.Snapshots != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			after, err := m.opts.Snapshots.Create(ctx)
			cancel()
			if err == nil {
				afterID = after.ID
			}
		}
		if _, ok := m.session.Undo(); !ok {
			m.notice("nothing to undo")
			break
		}
		m.undone = append(m.undone, undoState{Messages: before, Files: afterID})
		_ = m.opts.Sessions.Save(m.session)
		m.syncFromSession()
		if restoreID != "" && m.opts.Snapshots != nil {
			if err := m.restoreSnapshot(restoreID); err != nil {
				m.notice("undone (file restore failed: " + err.Error() + ")")
			} else {
				m.notice("undone (files restored)")
			}
		} else {
			m.notice("undone")
		}
	case "redo":
		if len(m.undone) == 0 {
			m.notice("nothing to redo")
			break
		}
		state := m.undone[len(m.undone)-1]
		m.undone = m.undone[:len(m.undone)-1]
		m.session.Messages = state.Messages
		_ = m.opts.Sessions.Save(m.session)
		m.syncFromSession()
		if state.Files != "" {
			if err := m.restoreSnapshot(state.Files); err != nil {
				m.notice("redone (file restore failed: " + err.Error() + ")")
				break
			}
		}
		m.notice("redone")
	case "compact":
		if m.streaming {
			m.notice("cannot compact while streaming")
			break
		}
		if len(m.session.Messages) == 0 {
			m.notice("nothing to compact")
			break
		}
		m.status = "compacting..."
		sess := m.session
		runner := m.opts.Runner
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_, err := runner.Compact(ctx, sess, m.modelID)
			return compactDone{err: err}
		}
	case "export":
		_ = m.opts.Sessions.Save(m.session)
		path := filepath.Join(m.opts.Directory, "subcoder-"+m.session.ID+".json")
		data, err := os.ReadFile(filepath.Join(m.opts.Sessions.Dir(), m.session.ID+".json"))
		if err == nil {
			err = os.WriteFile(path, data, 0o644)
		}
		if err != nil {
			m.notice("error: " + err.Error())
			break
		}
		m.notice("exported to " + path)
	case "exit", "quit":
		return tea.Quit
	default:
		custom, ok := command.Find(m.commands, cmd.Name)
		if !ok {
			m.notice("unknown command /" + cmd.Name + " (try /help)")
			break
		}
		expanded, err := command.Expand(custom, cmd.Raw, m.opts.Directory)
		if err != nil {
			m.notice("error: " + err.Error())
			break
		}
		if custom.Agent != "" {
			m.agentName = custom.Agent
			m.session.Agent = custom.Agent
		}
		if custom.Model != "" {
			m.modelID = custom.Model
			m.session.Model = custom.Model
		}
		_ = m.opts.Sessions.Save(m.session)
		m.notice("/" + custom.Name)
		return m.startRun(expanded)
	}
	return nil
}

func (m *model) restoreSnapshot(id string) error {
	if m.opts.Snapshots == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return m.opts.Snapshots.Restore(ctx, id)
}

func (m *model) openEditor(initial string) tea.Cmd {
	path, err := writePromptFile(initial)
	if err != nil {
		m.notice("editor: " + err.Error())
		return nil
	}
	command := exec.Command("sh", "-c", editorCommand()+" "+shellQuote(path))
	return tea.ExecProcess(command, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return editorDone{err: err}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return editorDone{err: readErr}
		}
		return editorDone{text: string(data)}
	})
}

func writePromptFile(content string) (string, error) {
	file, err := os.CreateTemp("", "subcoder-prompt-*.md")
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(content); err != nil {
		file.Close()
		os.Remove(file.Name())
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}

func editorCommand() string {
	for _, name := range []string{"SUBCODER_EDITOR", "VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return "vi"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func loadModelsCmd(providers *provider.Registry) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return modelsLoaded{models: providers.Models(ctx)}
	}
}

func (m *model) runShell(command string) tea.Cmd {
	if command == "" {
		m.notice("usage: !command")
		return nil
	}
	dir := m.opts.Directory
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		return shellDone{command: command, output: string(output), err: err}
	}
}

func (m *model) openPicker(kind pickerKind, title string, items []pickerItem) {
	m.picker = &picker{kind: kind, title: title, items: items}
}

func (m *model) selectPicker() tea.Cmd {
	p := m.picker
	if p == nil || len(p.items) == 0 {
		m.picker = nil
		return nil
	}
	item := p.items[p.cursor]
	m.picker = nil

	switch p.kind {
	case pickerModels:
		m.modelID = item.id
		m.session.Model = item.id
		_ = m.opts.Sessions.Save(m.session)
		m.notice("model: " + item.id)
	case pickerAgents:
		m.agentName = item.id
		m.session.Agent = item.id
		_ = m.opts.Sessions.Save(m.session)
		m.notice("agent: " + item.id)
	case pickerThemes:
		m.themeName = item.id
		m.styles = newTheme(item.id)
		m.notice("theme: " + item.id + " (set \"theme\" in config to persist)")
	case pickerSessions:
		if m.streaming {
			m.notice("cannot switch sessions while streaming")
			return nil
		}
		_ = m.opts.Sessions.Save(m.session)
		sess, err := m.opts.Sessions.Load(item.id)
		if err != nil {
			m.notice("error: " + err.Error())
			return nil
		}
		m.session = sess
		if sess.Model != "" {
			m.modelID = sess.Model
		}
		if sess.Agent != "" {
			m.agentName = sess.Agent
		}
		m.undone = nil
		m.syncFromSession()
		m.notice("session: " + sess.ID)
	}
	return nil
}

func (m *model) resolvePermission(reply permission.Reply) {
	if m.permReply == nil {
		return
	}
	select {
	case m.permReply <- reply:
	default:
	}
	m.permReq = nil
	m.permReply = nil
	m.rebuild()
}

func (m *model) finishRun(err error) {
	m.streaming = false
	m.cancelRun = nil
	m.status = ""

	if m.pendingSnapshot != nil && m.session != nil {
		for i := len(m.session.Messages) - 1; i >= 0; i-- {
			msg := &m.session.Messages[i]
			if msg.Role == "user" && !msg.Synthetic && msg.Snapshot == "" {
				msg.Snapshot = m.pendingSnapshot.ID
				_ = m.opts.Sessions.Save(m.session)
				break
			}
		}
	}
	m.pendingSnapshot = nil

	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		m.notice("interrupted")
	default:
		m.notice("error: " + err.Error())
	}

	if m.session != nil {
		m.syncFromSession()
	}
	m.rebuild()
}
