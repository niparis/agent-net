package tui

import (
	"context"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tui/graphview"
)

// Run shows the screen until the person quits or ctx ends. It uses the
// alternate screen and needs no mouse.
func Run(ctx context.Context, src Source, versions ...string) error {
	defer src.Close()
	// The TUI asks for the background from Init, as a Bubble Tea command.
	// Non-TUI commands never query the terminal.
	m := New(src)
	version := ""
	if len(versions) > 0 {
		version = versions[0]
	}
	attempted := restoreRestart(&m, version)
	path := restartExecutable()
	m.restart = &restartState{changed: executableChanged(path), version: version, attempted: attempted}
	m.graphPixel.set(graphview.Detect(os.Getenv, graphview.QueryTerminal))
	m.graphPixel.motion = os.Getenv("AGENT_NET_GRAPH_MOTION") == "1"
	m.herdrSocket = herdrSocketOf(os.Getenv("HERDR_ENV"), os.Getenv("HERDR_SOCKET_PATH"))
	if m.herdrSocket != "" {
		m.herdrPaneID = os.Getenv("HERDR_PANE_ID")
	}
	for {
		p := tea.NewProgram(m, tea.WithContext(ctx))
		if receiver, ok := src.(interface {
			SetUIPush(func(protocol.UIRequest))
		}); ok {
			receiver.SetUIPush(func(q protocol.UIRequest) { p.Send(q) })
		}
		final, err := p.Run()
		if err == nil && ctx.Err() == nil {
			m = final.(Model)
			if m.restart.requested {
				v := m.restart.target
				if v == "" {
					v = m.snap.Version
				}
				if v == "" {
					v = "unknown"
				}
				m.restart.target = v
				if m.execUpdate(path, os.Args, execRestart) != nil {
					m.restart.requested = false
					m.restart.failed = true
					m.fail("New version " + v + " installed; press q and start agent-net tui again")
					continue
				}
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
}

// herdrSocketOf is the herdr server socket when this terminal runs inside
// herdr (HERDR_ENV is 1), else "".
func herdrSocketOf(env, socket string) string {
	if env != "1" || socket == "" {
		return ""
	}
	return socket
}
