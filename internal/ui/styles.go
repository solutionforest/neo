package ui

import "github.com/charmbracelet/lipgloss"

var (
	Green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	Red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	Yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	Cyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	Gray   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	Bold   = lipgloss.NewStyle().Bold(true)
	Faint  = lipgloss.NewStyle().Faint(true)
)
