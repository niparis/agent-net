package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// A shared OpenCode server can discover the judge's saved session while the
// judge runs or after it is killed, without inheriting the judge's environment.
func TestJudgeOpenCodeDiscoveryAfterTimeout(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{exe: "/opt/opencode"})
	dir := t.TempDir()
	record := filepath.Join(dir, "run.json")
	saved := filepath.Join(dir, "sessions.json")
	deleted := filepath.Join(dir, "deleted")
	t.Setenv("FAKE_OPENCODE_SAVE_SESSION", "1")
	t.Setenv("FAKE_OPENCODE_SESSIONS", saved)
	t.Setenv("FAKE_OPENCODE_DELETED", deleted)
	t.Setenv(envFakeOpenCode, "1")
	t.Setenv("FAKE_OPENCODE_RUN_RECORD", record)
	t.Setenv("FAKE_DIFFICULTY_GATE", filepath.Join(dir, "never"))
	a := NewDifficultyAssessor(Config{HarnessPaths: map[string]string{"opencode": os.Args[0]}}, DifficultySources{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := a.runOpenCode(ctx, "fake/model", "brief", time.Second); done <- err }()
	var rec struct {
		Dir string `json:"dir"`
	}
	eventually(t, "judge saved its session", func() bool {
		raw, err := os.ReadFile(record)
		_, savedErr := os.Stat(saved)
		return err == nil && savedErr == nil && json.Unmarshal(raw, &rec) == nil && rec.Dir != ""
	})
	// Discovery sees the shared server's PID, not the private judge's PID.
	for _, phase := range []string{"running", "killed"} {
		if phase == "killed" {
			if err := <-done; err == nil {
				t.Fatal("judge did not time out")
			}
		}
		n.d.SetSessions("opencode", []protocol.SessionInfo{{NativeID: "ses_judge", PID: os.Getpid(), Cwd: rec.Dir, Status: "idle"}})
		if daemonSession(t, n, "opencode", "ses_judge") != nil {
			t.Errorf("%s judge appeared in discovery", phase)
		}
		lc := dialLocal(t, n.sock)
		var hello struct {
			SessionID string `json:"session_id"`
		}
		mustJSON(t, lc.mustCall("hello", map[string]any{"client": "opencode", "harness": "opencode", "native_id": "ses_plugin_judge", "cwd": rec.Dir, "status": "idle"}), &hello)
		if hello.SessionID != "" {
			t.Errorf("%s judge plugin registered %s", phase, hello.SessionID)
		}
		lc.c.Close()
	}
	eventually(t, "killed judge's saved session is deleted", func() bool {
		raw, err := os.ReadFile(deleted)
		return err == nil && string(raw) == "ses_judge\n"
	})
	n.d.SetSessions("opencode", []protocol.SessionInfo{{NativeID: "ses_owner", PID: os.Getpid(), Cwd: dir, Status: "idle"}})
	if daemonSession(t, n, "opencode", "ses_owner") == nil {
		t.Fatal("ordinary OpenCode session was hidden")
	}
}
