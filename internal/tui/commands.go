package tui

import (
	"fmt"
	"strings"

	"github.com/arthurkushman/subcoder/internal/command"
)

type slashCommand struct {
	Name string
	Args []string
	Raw  string
}

func parseCommand(input string) (slashCommand, bool) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return slashCommand{}, false
	}
	raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return slashCommand{}, false
	}
	return slashCommand{Name: strings.ToLower(fields[0]), Args: fields[1:], Raw: strings.Join(fields[1:], " ")}, true
}

type commandInfo struct {
	name        string
	description string
}

var commandList = []commandInfo{
	{"help", "Show available commands"},
	{"init", "Create an AGENTS.md file for the project"},
	{"new", "Start a new session"},
	{"sessions", "Switch to another session"},
	{"models", "Switch the model"},
	{"agents", "Switch the agent"},
	{"themes", "Switch the color theme"},
	{"commands", "List custom commands"},
	{"editor", "Compose the prompt in $EDITOR"},
	{"undo", "Revert the last turn and its file changes"},
	{"redo", "Restore the last undone turn"},
	{"compact", "Summarize the conversation to free context"},
	{"details", "Toggle expanded tool output"},
	{"thinking", "Toggle reasoning blocks"},
	{"timestamps", "Toggle message timestamps"},
	{"export", "Export the session to subcoder-<id>.json"},
	{"exit", "Quit SubCoder"},
}

func helpText(custom []command.Command) string {
	var sb strings.Builder
	sb.WriteString("Commands:\n")
	for _, info := range commandList {
		sb.WriteString(fmt.Sprintf("  /%-10s %s\n", info.name, info.description))
	}
	if len(custom) > 0 {
		sb.WriteString("\nCustom commands:\n")
		for _, cmd := range custom {
			sb.WriteString(fmt.Sprintf("  /%-10s %s\n", cmd.Name, cmd.Description))
		}
	}
	sb.WriteString("\nMentions: @path attaches a file to your prompt\n")
	sb.WriteString("Shortcuts: enter send · ctrl+j newline · tab switch agent · esc interrupt · ctrl+c quit\n")
	return sb.String()
}
