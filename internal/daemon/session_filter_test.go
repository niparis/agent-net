package daemon

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestJudgeDirSessionNotRegistered(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{})

	origProcEnv := processEnviron
	defer func() { processEnviron = origProcEnv }()
	processEnviron = func(pid int) []string {
		switch pid {
		case 54321:
			return []string{"PWD=/tmp/agent-net-difficulty-opencode-987654"}
		case 54322:
			return []string{IgnoreEnv + "=1"}
		case 54323:
			return []string{"PWD=/home/user/project"}
		default:
			return nil
		}
	}

	// Session whose process has PWD in a judge directory: not registered.
	n.d.SetSessions("claude", []protocol.SessionInfo{{NativeID: "ses_judge", PID: 54321, Cwd: "/tmp/agent-net-difficulty-opencode-987654"}})
	n.d.mu.Lock()
	_, registered := n.d.sessions[key("claude", "ses_judge")]
	n.d.mu.Unlock()
	if registered {
		t.Fatal("judge-dir session was registered")
	}

	// Session with AGENT_NET_IGNORE=1: not registered.
	n.d.SetSessions("claude", []protocol.SessionInfo{{NativeID: "ses_ignored", PID: 54322, Cwd: "/home/user/project"}})
	n.d.mu.Lock()
	_, registered = n.d.sessions[key("claude", "ses_ignored")]
	n.d.mu.Unlock()
	if registered {
		t.Fatal("ignored session was registered")
	}

	// Normal session: registered.
	n.d.SetSessions("claude", []protocol.SessionInfo{{NativeID: "ses_normal", PID: 54323, Cwd: "/home/user/project"}})
	n.d.mu.Lock()
	_, registered = n.d.sessions[key("claude", "ses_normal")]
	n.d.mu.Unlock()
	if !registered {
		t.Fatal("normal session was not registered")
	}
}
