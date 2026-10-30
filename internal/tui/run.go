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
	m.herdrSocket = herdrSocketOf(os.Getenv("HERDR_ENV"), os.Getenv("HERDR_SOCKET_PATH"))
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// herdrSocketOf is the herdr server socket when this terminal runs inside
// herdr (HERDR_ENV is 1), else "".
func herdrSocketOf(env, socket string) string {
	if env != "1" || socket == "" {
		return ""
	}
	return socket
}

// tmuxSocketOf is the socket path in a $TMUX value ("<socket>,<pid>,<index>").
func tmuxSocketOf(env string) string {
	sock, _, _ := strings.Cut(env, ",")
	return sock
}
