package frontmatter

import "strings"

// Document is a parsed markdown document with optional YAML-like frontmatter.
type Document struct {
	Fields map[string]string
	Body   string
}

// Parse splits a markdown document into its frontmatter fields and body.
// Frontmatter must start on the first line with "---" and end with "---".
// Only flat key: value pairs are supported, which is all agents, commands and
// skills need.
func Parse(content string) Document {
	content = strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(content, "---") {
		return Document{Fields: map[string]string{}, Body: strings.TrimSpace(content)}
	}

	rest := strings.TrimPrefix(content, "---")
	if !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r\n") {
		return Document{Fields: map[string]string{}, Body: strings.TrimSpace(content)}
	}
	rest = strings.TrimPrefix(strings.TrimPrefix(rest, "\n"), "\r\n")

	fields := map[string]string{}
	body := ""
	lines := strings.Split(rest, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			body = strings.Join(lines[i+1:], "\n")
			return Document{Fields: fields, Body: strings.TrimSpace(body)}
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		fields[key] = unquote(strings.TrimSpace(value))
	}
	return Document{Fields: map[string]string{}, Body: strings.TrimSpace(content)}
}

// List parses a frontmatter list such as "[read, grep, glob]" or
// "read, grep, glob" into its items.
func List(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	var items []string
	for _, item := range strings.Split(value, ",") {
		item = unquote(strings.TrimSpace(item))
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

func unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
