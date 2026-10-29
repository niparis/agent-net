package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// Run shows the screen until the person quits or ctx ends. It uses the
// alternate screen and needs no mouse.
func Run(ctx context.Context, src Source) error {
	defer src.Close()
	p := tea.NewProgram(New(src), tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
