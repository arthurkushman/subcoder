package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the interactive terminal UI.
func Run(opts Options) error {
	m := newModel(opts)
	program := tea.NewProgram(m, tea.WithAltScreen())

	if opts.Permission != nil {
		opts.Permission.SetAsker(&Asker{Program: program})
	}

	_, err := program.Run()
	return err
}

// NewModel builds a model without starting the program. Used by tests.
func NewModel(opts Options) *model {
	m := newModel(opts)
	return m
}
