package tui

import "github.com/charmbracelet/lipgloss"

type theme struct {
	base       lipgloss.Style
	header     lipgloss.Style
	accent     lipgloss.Style
	dim        lipgloss.Style
	user       lipgloss.Style
	assistant  lipgloss.Style
	reasoning  lipgloss.Style
	toolOK     lipgloss.Style
	toolErr    lipgloss.Style
	toolRun    lipgloss.Style
	toolBody   lipgloss.Style
	summary    lipgloss.Style
	notice     lipgloss.Style
	error      lipgloss.Style
	input      lipgloss.Style
	status     lipgloss.Style
	modal      lipgloss.Style
	modalSel   lipgloss.Style
	modalTitle lipgloss.Style
}

type palette struct {
	accent string
	user   string
	dim    string
	ok     string
	err    string
	warn   string
	body   string
	notice string
}

var themes = map[string]palette{
	"default":    {accent: "205", user: "39", dim: "241", ok: "42", err: "196", warn: "214", body: "245", notice: "111"},
	"dark":       {accent: "39", user: "39", dim: "241", ok: "42", err: "196", warn: "214", body: "245", notice: "111"},
	"light":      {accent: "25", user: "25", dim: "244", ok: "28", err: "160", warn: "130", body: "240", notice: "24"},
	"dracula":    {accent: "141", user: "212", dim: "243", ok: "84", err: "203", warn: "215", body: "245", notice: "117"},
	"nord":       {accent: "111", user: "109", dim: "244", ok: "150", err: "174", warn: "179", body: "246", notice: "110"},
	"gruvbox":    {accent: "208", user: "109", dim: "245", ok: "142", err: "167", warn: "214", body: "247", notice: "108"},
	"tokyonight": {accent: "117", user: "141", dim: "243", ok: "120", err: "203", warn: "221", body: "103", notice: "111"},
	"mono":       {accent: "250", user: "252", dim: "244", ok: "250", err: "252", warn: "248", body: "245", notice: "248"},
}

func themeNames() []string {
	return []string{"default", "dark", "light", "dracula", "nord", "gruvbox", "tokyonight", "mono"}
}

func newTheme(name string) theme {
	p, ok := themes[name]
	if !ok {
		p = themes["default"]
	}

	return theme{
		base:       lipgloss.NewStyle(),
		header:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(p.accent)),
		accent:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.accent)),
		dim:        lipgloss.NewStyle().Foreground(lipgloss.Color(p.dim)),
		user:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(p.user)),
		assistant:  lipgloss.NewStyle(),
		reasoning:  lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color(p.dim)),
		toolOK:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.ok)),
		toolErr:    lipgloss.NewStyle().Foreground(lipgloss.Color(p.err)),
		toolRun:    lipgloss.NewStyle().Foreground(lipgloss.Color(p.warn)),
		toolBody:   lipgloss.NewStyle().Foreground(lipgloss.Color(p.body)),
		summary:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(p.accent)).Padding(0, 1),
		notice:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.notice)),
		error:      lipgloss.NewStyle().Foreground(lipgloss.Color(p.err)).Bold(true),
		input:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(p.dim)),
		status:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.dim)),
		modal:      lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(p.accent)).Padding(0, 1),
		modalSel:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(p.accent)),
		modalTitle: lipgloss.NewStyle().Bold(true),
	}
}
