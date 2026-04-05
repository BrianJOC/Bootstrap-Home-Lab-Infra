package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// Stage state icons.
	iconPending = "○"
	iconRunning = "◉"
	iconDone    = "✓"
	iconFailed  = "✗"
	iconSkipped = "–"

	// Stage state styles.
	stylePending = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Faint(true)

	styleRunning = lipgloss.NewStyle().
			Foreground(lipgloss.Color("220")).
			Bold(true)

	styleDone = lipgloss.NewStyle().
			Foreground(lipgloss.Color("40"))

	styleFailed = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	styleSkipped = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Faint(true)

	// Log pane styles.
	styleLogPane = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("237")).
			Padding(0, 1)

	styleLogLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Bold(false).
			Faint(true)

	// Error panel style.
	styleErrorPanel = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("196")).
			Foreground(lipgloss.Color("196")).
			Bold(true).
			Padding(0, 1)

	// Stage list pane.
	styleStagePane = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Width(28).
			Padding(0, 1)

	// Header style.
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("33")).
			Padding(0, 1)

	// Input prompt style.
	stylePromptLabel = lipgloss.NewStyle().
				Foreground(lipgloss.Color("33")).
				Bold(true)

	// Warning box style.
	styleWarningBox = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("220")).
			Foreground(lipgloss.Color("220")).
			Bold(true).
			Padding(0, 1)
)

// renderStageRow renders a single stage row with icon and label.
func renderStageRow(name, state string) string {
	var icon, label string
	switch state {
	case "running":
		icon = styleRunning.Render(iconRunning)
		label = styleRunning.Render(name)
	case "done":
		icon = styleDone.Render(iconDone)
		label = styleDone.Render(name)
	case "failed":
		icon = styleFailed.Render(iconFailed)
		label = styleFailed.Render(name)
	case "skipped":
		icon = styleSkipped.Render(iconSkipped)
		label = styleSkipped.Render(name)
	default:
		icon = stylePending.Render(iconPending)
		label = stylePending.Render(name)
	}
	return icon + " " + label
}
