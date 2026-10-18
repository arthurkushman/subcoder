package tui

import (
	"fmt"
	"strings"

	"github.com/arthurkushman/subcoder/internal/session"
	"github.com/charmbracelet/lipgloss"
)

func lipglossDim() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
}

func (m *model) View() string {
	if !m.ready {
		return "Starting SubCoder..."
	}

	header := m.headerView()
	body := m.view.View()

	if m.picker != nil {
		body = m.pickerView()
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n")
	sb.WriteString(body)
	sb.WriteString("\n")

	if m.permReq != nil {
		sb.WriteString(m.permissionView())
		sb.WriteString("\n")
	}

	sb.WriteString(m.styles.input.Width(max(20, m.width-2)).Render(m.input.View()))
	sb.WriteString("\n")
	sb.WriteString(m.styles.status.Width(max(20, m.width-2)).Render(m.statusView()))

	return sb.String()
}

func (m *model) headerView() string {
	title := m.styles.header.Render(" SubCoder ")
	model := m.styles.accent.Render(m.modelID)
	agentName := m.styles.dim.Render("· " + m.agentName)
	sessionTitle := ""
	if m.title != "" {
		sessionTitle = m.styles.dim.Render("· " + truncate(m.title, max(10, m.width-len(m.modelID)-len(m.agentName)-20)))
	}
	return fmt.Sprintf("%s %s %s %s", title, model, agentName, sessionTitle)
}

func (m *model) statusView() string {
	var parts []string
	if m.streaming {
		parts = append(parts, m.spinner.View()+" "+m.status)
	} else if m.status != "" {
		parts = append(parts, m.status)
	}

	usage := m.totalUsage()
	if usage.Input > 0 || usage.Output > 0 {
		parts = append(parts, fmt.Sprintf("tokens %d/%d", int(usage.Input), int(usage.Output)))
	}
	if usage.Cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", usage.Cost))
	}
	if len(parts) == 0 {
		parts = append(parts, "ready · enter send · @file context · tab agent · esc interrupt · ctrl+c quit")
	}
	return " " + strings.Join(parts, " · ")
}

func (m *model) totalUsage() session.Usage {
	var total session.Usage
	for _, msg := range m.messages {
		total.Add(msg.Usage)
	}
	return total
}

func (m *model) pickerView() string {
	p := m.picker
	var sb strings.Builder
	sb.WriteString(m.styles.modalTitle.Render(p.title) + "\n\n")

	if len(p.items) == 0 {
		sb.WriteString(m.styles.dim.Render("(empty)") + "\n")
	}
	maxItems := max(5, m.view.Height-6)
	start := 0
	if p.cursor >= maxItems {
		start = p.cursor - maxItems + 1
	}
	for i := start; i < len(p.items) && i < start+maxItems; i++ {
		item := p.items[i]
		line := fmt.Sprintf("%-40s %s", truncate(item.label, 40), m.styles.dim.Render(truncate(item.description, 60)))
		if i == p.cursor {
			sb.WriteString(m.styles.modalSel.Render("> "+line) + "\n")
		} else {
			sb.WriteString("  " + line + "\n")
		}
	}
	sb.WriteString("\n" + m.styles.dim.Render("up/down select · enter confirm · esc cancel"))

	height := m.view.Height
	if height < 8 {
		height = 8
	}
	return m.styles.modal.Height(height - 2).Width(max(30, m.view.Width-4)).Render(sb.String())
}

func (m *model) permissionView() string {
	req := m.permReq
	var sb strings.Builder
	sb.WriteString(m.styles.modalTitle.Render("Permission required") + "\n\n")
	sb.WriteString(req.Title + "\n")
	if req.Details != "" {
		details := truncate(strings.ReplaceAll(req.Details, "\n", " "), max(30, m.width-10))
		sb.WriteString(m.styles.dim.Render(details) + "\n")
	}
	sb.WriteString("\n" + m.styles.accent.Render("[y] once  [a] always  [n] reject"))
	return m.styles.modal.Render(sb.String())
}

func (m *model) rebuild() {
	if !m.ready {
		return
	}

	width := m.view.Width - 2
	if width < 20 {
		width = 20
	}

	var lines []string
	for _, msg := range m.messages {
		if msg.Synthetic {
			continue
		}
		prefix := ""
		if m.showTimestamps && !msg.Time.IsZero() {
			prefix = m.styles.dim.Render(msg.Time.Local().Format("15:04") + " ")
		}
		switch msg.Role {
		case "user":
			text := strings.TrimSpace(msg.Text())
			if text == "" {
				continue
			}
			lines = append(lines, prefix+m.styles.user.Render("› ")+wrap(text, width-2))
			lines = append(lines, "")
		case "assistant":
			lines = append(lines, m.renderAssistant(msg, width, prefix)...)
			lines = append(lines, "")
		}
	}

	for _, notice := range m.notices {
		lines = append(lines, m.styles.notice.Render("· "+notice))
	}

	m.view.SetContent(strings.Join(lines, "\n"))
	m.view.GotoBottom()
}

func (m *model) renderAssistant(msg session.Message, width int, prefix string) []string {
	var lines []string
	first := true
	add := func(line string) {
		if first {
			line = prefix + line
			first = false
		}
		lines = append(lines, line)
	}

	for _, part := range msg.Parts {
		switch part.Type {
		case session.PartText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			add(wrap(part.Text, width))
		case session.PartSummary:
			add(m.styles.summary.Width(width - 4).Render(truncate(part.Text, 1200)))
		case session.PartReasoning:
			if !m.showThinking || strings.TrimSpace(part.Text) == "" {
				continue
			}
			add(m.styles.reasoning.Render(wrap("∴ "+part.Text, width-2)))
		case session.PartTool:
			if part.Tool == nil {
				continue
			}
			for _, line := range m.renderTool(part.Tool, width) {
				add(line)
			}
		}
	}

	if msg.Error != "" {
		add(m.styles.error.Render("error: " + msg.Error))
	}
	return lines
}

func (m *model) renderTool(call *session.ToolCall, width int) []string {
	icon := "○"
	style := m.styles.toolRun
	switch call.State {
	case session.ToolRunning:
		icon = "◐"
		style = m.styles.toolRun
	case session.ToolCompleted:
		icon = "✓"
		style = m.styles.toolOK
	case session.ToolError:
		icon = "✗"
		style = m.styles.toolErr
	}

	title := call.Title
	if title == "" {
		title = call.Name
	}
	lines := []string{style.Render(icon + " " + truncate(title, width-4))}

	body := call.Output
	if call.State == session.ToolError {
		body = call.Error
	}
	if strings.TrimSpace(body) == "" {
		return lines
	}

	bodyLines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	limit := 6
	if m.details {
		limit = 40
	}
	for i, line := range bodyLines {
		if i >= limit {
			lines = append(lines, m.styles.toolBody.Render(fmt.Sprintf("  … %d more lines (/details to expand)", len(bodyLines)-limit)))
			break
		}
		lines = append(lines, m.styles.toolBody.Render("  "+truncate(line, width-4)))
	}
	return lines
}

func wrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var out []string
	for _, paragraph := range strings.Split(text, "\n") {
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if len(line)+1+len(word) > width {
				out = append(out, line)
				line = word
				continue
			}
			line += " " + word
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
