package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arthurkushman/subcoder/internal/config"
)

const FileName = "AGENTS.md"

type Rule struct {
	Path    string
	Content string
}

// Discover collects AGENTS.md files walking up from dir, ending with the
// global config directory. Files closer to the project win.
func Discover(dir string) []Rule {
	var found []Rule
	seen := map[string]bool{}

	cur := dir
	for {
		path := filepath.Join(cur, FileName)
		if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			found = append(found, Rule{Path: path, Content: strings.TrimSpace(string(data))})
			seen[path] = true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	global := filepath.Join(config.ConfigDir(), FileName)
	if !seen[global] {
		if data, err := os.ReadFile(global); err == nil && len(strings.TrimSpace(string(data))) > 0 {
			found = append(found, Rule{Path: global, Content: strings.TrimSpace(string(data))})
		}
	}

	return found
}

func Combine(ruleList []Rule) string {
	if len(ruleList) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, rule := range ruleList {
		fmt.Fprintf(&sb, "Instructions from: %s\n%s\n\n", rule.Path, rule.Content)
	}
	return strings.TrimSpace(sb.String())
}

// Init generates an AGENTS.md starter document for a project directory based
// on a quick scan of its contents.
func Init(dir string) string {
	name := filepath.Base(dir)

	var markers []string
	detected := detectLanguages(dir)
	for _, lang := range detected {
		markers = append(markers, lang.Marker)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", name)
	sb.WriteString("## Project overview\n\n")
	if len(detected) > 0 {
		names := make([]string, 0, len(detected))
		for _, lang := range detected {
			names = append(names, lang.Name)
		}
		fmt.Fprintf(&sb, "This project is written in %s.\n\n", strings.Join(names, ", "))
	} else {
		sb.WriteString("Describe what this project does and the main modules here.\n\n")
	}

	sb.WriteString("## Commands\n\n")
	if len(detected) > 0 {
		sb.WriteString("| Task | Command |\n| --- | --- |\n")
		for _, lang := range detected {
			for _, cmd := range lang.Commands {
				parts := strings.SplitN(cmd, "|", 2)
				if len(parts) == 2 {
					fmt.Fprintf(&sb, "| %s | `%s` |\n", parts[0], parts[1])
				}
			}
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("- Build: \n- Test: \n- Lint: \n\n")
	}

	sb.WriteString("## Project layout\n\n")
	entries, err := os.ReadDir(dir)
	if err == nil {
		count := 0
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			if entry.IsDir() {
				fmt.Fprintf(&sb, "- `%s/` - \n", entry.Name())
			} else {
				fmt.Fprintf(&sb, "- `%s` - \n", entry.Name())
			}
			count++
			if count >= 20 {
				break
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Code style\n\n")
	sb.WriteString("- Follow the existing style of the codebase.\n")
	sb.WriteString("- Prefer small, focused changes with tests.\n\n")

	sb.WriteString("## Notes for agents\n\n")
	sb.WriteString("- Run the test suite before claiming work is done.\n")
	sb.WriteString("- Never commit secrets or generated artifacts.\n")

	return sb.String()
}

type language struct {
	Name     string
	Marker   string
	Commands []string
}

func detectLanguages(dir string) []language {
	candidates := []language{
		{Name: "Go", Marker: "go.mod", Commands: []string{"Build|go build ./...", "Test|go test ./...", "Vet|go vet ./..."}},
		{Name: "TypeScript/JavaScript", Marker: "package.json", Commands: []string{"Install|npm install", "Test|npm test", "Build|npm run build"}},
		{Name: "Rust", Marker: "Cargo.toml", Commands: []string{"Build|cargo build", "Test|cargo test"}},
		{Name: "Python", Marker: "pyproject.toml", Commands: []string{"Test|pytest"}},
		{Name: "Ruby", Marker: "Gemfile", Commands: []string{"Test|bundle exec rspec"}},
	}

	var detected []language
	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Join(dir, candidate.Marker)); err == nil {
			detected = append(detected, candidate)
		}
	}
	if len(detected) == 0 {
		if matches, _ := filepath.Glob(filepath.Join(dir, "*.py")); len(matches) > 0 {
			detected = append(detected, language{Name: "Python", Marker: "*.py", Commands: []string{"Test|pytest"}})
		}
	}
	sort.Slice(detected, func(i, j int) bool { return detected[i].Name < detected[j].Name })
	return detected
}
