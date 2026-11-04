package tui

import (
	"context"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
	"github.com/niparis/agent-net/internal/tui/palette"
)

// Run shows the screen until the person quits or ctx ends. It uses the
// alternate screen and needs no mouse.
func Run(ctx context.Context, src Source) error {
	defer src.Close()
	// Resolve Lip Gloss's adaptive colors before Bubble Tea acquires the
	// terminal. Non-TUI commands never need to ask the terminal for its palette.
	palette.SetDark(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
	m := New(src)
	m.graphPixel.set(graphview.Detect(os.Getenv, graphview.QueryTerminal))
	m.graphPixel.motion = os.Getenv("AGENT_NET_GRAPH_MOTION") == "1"
	m.tmuxSocket = tmuxSocketOf(os.Getenv("TMUX"))
	m.herdrSocket = herdrSocketOf(os.Getenv("HERDR_ENV"), os.Getenv("HERDR_SOCKET_PATH"))
	if m.herdrSocket != "" {
		m.herdrPaneID = os.Getenv("HERDR_PANE_ID")
	}
	p := tea.NewProgram(m, tea.WithContext(ctx))
	if receiver, ok := src.(interface {
		SetUIPush(func(protocol.UIRequest))
	}); ok {
		receiver.SetUIPush(func(q protocol.UIRequest) { p.Send(q) })
	}
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
