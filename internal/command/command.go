package command

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/arthurkushman/subcoder/internal/frontmatter"
	"github.com/arthurkushman/subcoder/internal/prompt"
)

// Command is a user-defined markdown slash command.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Model       string `json:"model,omitempty"`
	Template    string `json:"template,omitempty"`
	Path        string `json:"path,omitempty"`
}

var (
	argumentPattern = regexp.MustCompile(`\$ARGUMENTS|\$([1-9])`)
	shellPattern    = regexp.MustCompile("!`([^`]+)`")
	includePattern  = regexp.MustCompile(`(^|\s)@([^\s]+)`)
)

// Load discovers commands in the project and global command directories.
// Project commands override global commands with the same name.
func Load(directory string) []Command {
	byName := map[string]Command{}
	for _, dir := range []string{
		filepath.Join(config.ConfigDir(), "command"),
		filepath.Join(directory, ".subcoder", "command"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue
			}
			cmd := parse(strings.TrimSuffix(entry.Name(), ".md"), string(data))
			cmd.Path = filepath.Join(dir, entry.Name())
			byName[cmd.Name] = cmd
		}
	}

	commands := make([]Command, 0, len(byName))
	for _, cmd := range byName {
		commands = append(commands, cmd)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	return commands
}

func parse(name, content string) Command {
	doc := frontmatter.Parse(content)
	return Command{
		Name:        name,
		Description: doc.Fields["description"],
		Agent:       doc.Fields["agent"],
		Model:       doc.Fields["model"],
		Template:    doc.Body,
	}
}

func Find(commands []Command, name string) (Command, bool) {
	for _, cmd := range commands {
		if cmd.Name == name {
			return cmd, true
		}
	}
	return Command{}, false
}

// Expand renders a command template: $ARGUMENTS and $1..$9 are replaced with
// the provided arguments, !`shell` snippets are executed in the project
// directory, and @file references are embedded into the prompt.
func Expand(cmd Command, args, directory string) (string, error) {
	template := cmd.Template
	fields := strings.Fields(args)

	template = argumentPattern.ReplaceAllStringFunc(template, func(match string) string {
		if match == "$ARGUMENTS" {
			return args
		}
		index := int(match[1] - '1')
		if index < len(fields) {
			return fields[index]
		}
		return ""
	})

	hasArguments := strings.Contains(cmd.Template, "$ARGUMENTS") || strings.Contains(cmd.Template, "$1")

	var shellErr error
	template = shellPattern.ReplaceAllStringFunc(template, func(match string) string {
		sub := shellPattern.FindStringSubmatch(match)
		output, err := runShell(directory, sub[1])
		if err != nil {
			shellErr = err
			return ""
		}
		return output
	})
	if shellErr != nil {
		return "", shellErr
	}

	var includeErr error
	template = includePattern.ReplaceAllStringFunc(template, func(match string) string {
		sub := includePattern.FindStringSubmatch(match)
		leading := sub[1]
		block, _, err := prompt.FileBlock(directory, sub[2])
		if err != nil {
			includeErr = fmt.Errorf("command %s: %w", cmd.Name, err)
			return match
		}
		return leading + "\n" + block
	})
	if includeErr != nil {
		return "", includeErr
	}

	template = strings.TrimSpace(template)
	if args != "" && !hasArguments {
		template += "\n\nArguments: " + args
	}
	return template, nil
}

func runShell(directory, command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, "sh", "-c", command)
	process.Dir = directory
	output, err := process.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command shell %q: %w: %s", command, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimRight(string(output), "\n"), nil
}
