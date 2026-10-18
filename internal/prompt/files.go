package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxFileSize  = 128 << 10
	MaxTotalSize = 512 << 10
)

var mentionPattern = regexp.MustCompile(`(^|\s)@([^\s]+)`)

// FileBlock renders a file reference for inclusion in a model prompt.
func FileBlock(baseDir, ref string) (string, string, error) {
	path := ref
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, ref)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("%s is a directory", ref)
	}
	if info.Size() > MaxFileSize {
		return "", "", fmt.Errorf("%s is too large (%d bytes)", ref, info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return "", "", fmt.Errorf("%s is not a text file", ref)
	}
	rel := ref
	if abs, err := filepath.Abs(path); err == nil {
		if cwd, err := filepath.Abs(baseDir); err == nil {
			if within, err := filepath.Rel(cwd, abs); err == nil && !strings.HasPrefix(within, "..") {
				rel = within
			}
		}
	}
	block := fmt.Sprintf("<file path=%q>\n%s\n</file>", filepath.ToSlash(rel), strings.TrimRight(string(data), "\n"))
	return block, rel, nil
}

// FileContext extracts @path mentions from text and returns the referenced
// files as a prompt context block together with the list of included paths.
// Missing or unreadable paths are left in the text and skipped.
func FileContext(text, baseDir string) (string, []string) {
	matches := mentionPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return "", nil
	}

	var blocks []string
	var included []string
	seen := map[string]bool{}
	total := 0

	for _, match := range matches {
		ref := text[match[4]:match[5]]
		candidate, suffix := splitPunctuation(ref)
		block, rel, err := FileBlock(baseDir, candidate)
		if err != nil {
			if suffix == "" {
				continue
			}
			block, rel, err = FileBlock(baseDir, ref)
			if err != nil {
				continue
			}
		}
		if seen[rel] {
			continue
		}
		if total+len(block) > MaxTotalSize {
			break
		}
		seen[rel] = true
		total += len(block)
		blocks = append(blocks, block)
		included = append(included, rel)
	}

	return strings.Join(blocks, "\n\n"), included
}

// splitPunctuation separates trailing sentence punctuation from a mention so
// that "@main.go, please" resolves to "main.go". It returns the cleaned path
// and the removed suffix when the cleaned path exists.
func splitPunctuation(ref string) (string, string) {
	trimmed := strings.TrimRight(ref, ".,;:!?)]}")
	if trimmed == "" || trimmed == ref {
		return ref, ""
	}
	return trimmed, ref[len(trimmed):]
}
