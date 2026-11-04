package tui

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// newTextInput retains the original input palette. Bubbles v2 defaults to
// coloring prompts and unfocused text, whereas these fields used terminal text.
func newTextInput() textinput.Model {
	input := textinput.New()
	styles := input.Styles()
	styles.Focused.Prompt = lipgloss.NewStyle()
	styles.Blurred.Prompt = lipgloss.NewStyle()
	styles.Focused.Text = lipgloss.NewStyle()
	styles.Blurred.Text = lipgloss.NewStyle()
	styles.Cursor.Color = lipgloss.NoColor{}
	input.SetStyles(styles)
	return input
}
