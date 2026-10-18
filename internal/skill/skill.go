package skill

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/arthurkushman/subcoder/internal/config"
	"github.com/arthurkushman/subcoder/internal/frontmatter"
)

const FileName = "SKILL.md"

// Skill is a reusable instruction bundle following the SKILL.md convention:
// a directory containing SKILL.md with name/description frontmatter.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path,omitempty"`
	Body        string `json:"body,omitempty"`
}

// Load discovers skills in the project and global skill directories. Project
// skills override global skills with the same name.
func Load(directory string) []Skill {
	byName := map[string]Skill{}
	for _, dir := range []string{
		filepath.Join(config.ConfigDir(), "skill"),
		filepath.Join(directory, ".subcoder", "skill"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name(), FileName)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			skill := parse(entry.Name(), string(data))
			skill.Path = path
			byName[skill.Name] = skill
		}
	}

	skills := make([]Skill, 0, len(byName))
	for _, skill := range byName {
		skills = append(skills, skill)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

func parse(dirName, content string) Skill {
	doc := frontmatter.Parse(content)
	name := strings.TrimSpace(doc.Fields["name"])
	if name == "" {
		name = dirName
	}
	description := strings.TrimSpace(doc.Fields["description"])
	if description == "" {
		for _, line := range strings.Split(doc.Body, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				description = line
				break
			}
		}
	}
	return Skill{Name: name, Description: description, Body: doc.Body}
}

func Find(skills []Skill, name string) (Skill, bool) {
	for _, skill := range skills {
		if skill.Name == name {
			return skill, true
		}
	}
	return Skill{}, false
}

// Names returns the available skill names.
func Names(skills []Skill) []string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return names
}
