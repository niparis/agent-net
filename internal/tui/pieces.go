package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"strings"
)

func statusLabel(status string) string {
	switch strings.TrimSpace(status) {
	case "busy":
		return lipgloss.NewStyle().Foreground(roleBase).Background(roleGood).Render(status)
	case "needs you":
		return lipgloss.NewStyle().Foreground(roleBase).Background(roleWarning).Render(status)
	default:
		return sDim.Render(status)
	}
}

func panel(title string, body []string, width int, color lipgloss.TerminalColor) []string {
	return panelWithBorder(title, body, width, color, roleFaint)
}

func panelWithBorder(title string, body []string, width int, color, borderColor lipgloss.TerminalColor) []string {
	width = max(width, 4)
	border := lipgloss.NewStyle().Foreground(borderColor)
	heading := lipgloss.NewStyle().Foreground(color).Bold(true).Render(clip(title, width-4))
	top := border.Render("╭─") + heading + border.Render(strings.Repeat("─", max(width-3-ansi.StringWidth(heading), 0))+"╮")
	out := []string{top}
	for _, line := range body {
		out = append(out, border.Render("│")+padStyled(clip(line, width-2), width-2)+border.Render("│"))
	}
	return append(out, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
}

func keyHints(parts ...string) string {
	var out []string
	for i := 0; i+1 < len(parts); i += 2 {
		out = append(out, lipgloss.NewStyle().Foreground(roleAccent).Render(parts[i])+" "+sDim.Render(parts[i+1]))
	}
	return strings.Join(out, "  ")
}

func progressBar(frac float64, n int, color lipgloss.TerminalColor) string {
	n = max(n, 0)
	filled := min(max(int(frac*float64(n)+0.5), 0), n)
	if frac > 0 && n > 0 && filled == 0 {
		filled = 1
	}
	return lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled)) + lipgloss.NewStyle().Foreground(roleFaint).Render(strings.Repeat("░", n-filled))
}

// selectedRow restores the background after each embedded style reset.
func selectedRow(line string) string {
	sample := lipgloss.NewStyle().Background(roleSelection).Render("x")
	i := strings.Index(sample, "x")
	if i <= 0 {
		return line
	}
	prefix := sample[:i]
	return prefix + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+prefix) + "\x1b[0m"
}
