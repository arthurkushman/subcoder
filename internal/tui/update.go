package tui

import (
	"strings"

	"github.com/arthurkushman/subcoder/internal/permission"
	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func (m *model) Init() tea.Cmd {
	if !m.ready {
		m.initialize()
		m.ready = true
	}
	m.rebuild()

	cmds := []tea.Cmd{m.input.Focus(), m.spinner.Tick, waitForEvent(m.events)}
	if m.opts.Prompt != "" {
		prompt := m.opts.Prompt
		m.opts.Prompt = ""
		cmds = append(cmds, m.submit(prompt))
	}
	return tea.Batch(cmds...)
}

func waitForEvent(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		inputHeight := 5
		headerHeight := 1
		footerHeight := 1
		viewHeight := msg.Height - inputHeight - headerHeight - footerHeight
		if viewHeight < 3 {
			viewHeight = 3
		}
		m.view.Width = msg.Width - 2
		m.view.Height = viewHeight
		m.input.SetWidth(msg.Width - 4)
		m.rebuild()
		return m, nil

	case tea.KeyMsg:
		if m.permReq != nil {
			return m.handlePermissionKey(msg)
		}
		if m.picker != nil {
			return m.handlePickerKey(msg)
		}

		switch msg.String() {
		case "ctrl+c":
			if m.cancelRun != nil {
				m.cancelRun()
			}
			return m, tea.Quit
		case "esc":
			if m.cancelRun != nil {
				m.cancelRun()
			}
			return m, nil
		case "tab":
			m.cycleAgent()
			return m, nil
		case "enter":
			value := m.input.Value()
			m.input.Reset()
			return m, m.submit(value)
		case "ctrl+j":
			m.input.InsertString("\n")
			return m, nil
		}

		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case eventMsg:
		event := session.Event(msg)
		m.applyEvent(event)
		if event.Type == session.EventSessionIdle {
			m.streaming = false
			m.status = ""
			if m.session != nil {
				m.syncFromSession()
			}
			m.rebuild()
		}
		return m, waitForEvent(m.events)

	case runFinished:
		m.finishRun(msg.err)
		return m, waitForEvent(m.events)

	case permissionMsg:
		req := msg.req
		m.permReq = &req
		m.permReply = msg.reply
		m.rebuild()
		return m, nil

	case modelsLoaded:
		if len(msg.models) == 0 {
			m.notice("no models available; configure a provider first")
			return m, nil
		}
		items := make([]pickerItem, 0, len(msg.models))
		for _, model := range msg.models {
			items = append(items, pickerItem{
				id:          model.FullID(),
				label:       model.FullID(),
				description: model.Name,
			})
		}
		m.openPicker(pickerModels, "Models", items)
		return m, nil

	case shellDone:
		output := msg.output
		if len(output) > 4000 {
			output = output[:4000] + "..."
		}
		if msg.err != nil && output == "" {
			output = msg.err.Error()
		}
		m.notice("$ " + msg.command + "\n" + output)
		return m, nil

	case editorDone:
		if msg.err != nil {
			m.notice("editor: " + msg.err.Error())
			return m, nil
		}
		if strings.TrimSpace(msg.text) == "" {
			return m, nil
		}
		return m, m.submit(msg.text)

	case compactDone:
		m.status = ""
		if msg.err != nil {
			m.notice("error: " + msg.err.Error())
		} else {
			if loaded, err := m.opts.Sessions.Load(m.session.ID); err == nil {
				m.session = loaded
			}
			m.syncFromSession()
			m.notice("compacted session history")
		}
		m.rebuild()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.streaming {
			return m, cmd
		}
		return m, nil
	}

	return m, nil
}

func (m *model) handlePermissionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.resolvePermission(permission.Reply{Allowed: true})
	case "a", "A":
		m.resolvePermission(permission.Reply{Allowed: true, Always: true})
	default:
		m.resolvePermission(permission.Reply{Allowed: false})
	}
	return m, nil
}

func (m *model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.picker = nil
	case "up", "k":
		if m.picker.cursor > 0 {
			m.picker.cursor--
		}
	case "down", "j":
		if m.picker.cursor < len(m.picker.items)-1 {
			m.picker.cursor++
		}
	case "enter":
		return m, m.selectPicker()
	}
	m.rebuild()
	return m, nil
}
