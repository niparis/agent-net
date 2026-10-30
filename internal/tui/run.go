package tui

import (
	"context"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Run shows the screen until the person quits or ctx ends. It uses the
// alternate screen and needs no mouse.
func Run(ctx context.Context, src Source) error {
	defer src.Close()
	m := New(src)
	m.tmuxSocket = tmuxSocketOf(os.Getenv("TMUX"))
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// tmuxSocketOf is the socket path in a $TMUX value ("<socket>,<pid>,<index>").
func tmuxSocketOf(env string) string {
	sock, _, _ := strings.Cut(env, ",")
	return sock
}
