package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// stateDaemon is a daemon over a state folder with no gateway and no socket,
// so it runs where unix sockets are blocked.
func stateDaemon(t *testing.T, state, claudeSessions string) *Daemon {
	t.Helper()
	d, err := New(Config{StateDir: state, ClaudeSessionsDir: claudeSessions, SocketPath: filepath.Join(state, "unused.sock")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.claudeChecks.Wait(); d.st.close() })
	return d
}

// rebuildMachine is a daemon over a state folder with no gateway and no
// socket. Its Claude sessions live under claude/sessions, so their
// transcripts are under claude/projects, as on a real machine.
type rebuildMachine struct {
	t        *testing.T
	state    string
	sessions string
	projects string
	d        *Daemon
	mu       sync.Mutex
	kept     []protocol.OwnerWordsKeep // what the daemon sent to the gateway
}

func newRebuildMachine(t *testing.T) *rebuildMachine {
	t.Helper()
	root := t.TempDir()
	m := &rebuildMachine{t: t, state: filepath.Join(root, "state"), sessions: filepath.Join(root, "claude", "sessions"),
		projects: filepath.Join(root, "claude", "projects", "-code-shop")}
	if err := os.MkdirAll(m.projects, 0o700); err != nil {
		t.Fatal(err)
	}
	m.restart()
	return m
}

// restart is a new daemon over the same state folder. The prompts the last
// daemon kept in memory are gone, as after a real restart.
func (m *rebuildMachine) restart() {
	m.t.Helper()
	if m.d != nil {
		m.d.bg.Wait()
		m.d.st.close()
	}
	m.d = stateDaemon(m.t, m.state, m.sessions)
	m.d.ctoCtx = m.t.Context()
	m.d.cfg.testGatewayCall = func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		switch typ {
		case protocol.FrameOwnerWordsKeep:
			m.mu.Lock()
			m.kept = append(m.kept, params.(protocol.OwnerWordsKeep))
			m.mu.Unlock()
			return json.RawMessage(`{}`), true, nil
		case protocol.FrameOwnerWordsFind:
			return json.RawMessage(`{}`), true, nil
		}
		return nil, false, nil
	}
}

// session registers a local session as the gateway would name it.
func (m *rebuildMachine) session(harness, native, sid string) {
	m.d.mu.Lock()
	m.d.sessions[key(harness, native)] = &localSession{info: protocol.SessionInfo{Harness: harness, NativeID: native, Cwd: "/code/shop"}, sessionID: sid}
	m.d.mu.Unlock()
}

func (m *rebuildMachine) transcript(native string, entries ...map[string]any) {
	m.t.Helper()
	writeClaudeTranscript(m.t, m.projects, native, entries...)
}

func (m *rebuildMachine) sentToGateway(session, text string) bool {
	m.d.bg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.kept {
		if k.Session == session && k.Text == text {
			return true
		}
	}
	return false
}

// typedAt is a user entry the owner typed at a time.
func typedAt(promptID, text string, at time.Time) map[string]any {
	e := claudeUserEntry(promptID, text, typedOrigin)
	e["timestamp"] = at.UTC().Format(time.RFC3339Nano)
	return e
}

const lead, leadSID, otherSID = "1f1f1f1f-0000-4000-8000-00000000aa01", "s_lead", "s_other"

// After a restart the daemon reads the owner's typed prompts of the last
// window back from the Claude transcript. A quote from another session of the
// machine finds them, with the same filters as the prompt hook: no injected
// turn, no pasted text, no turn Claude Code marks as meta, nothing older than
// the window.
func TestOwnerWordsAreRebuiltFromTheTranscriptAfterARestart(t *testing.T) {
	m := newRebuildMachine(t)
	now := time.Now()
	old := typedAt("p-old", "use the old machine for this", now.Add(-ownerPromptWindow-time.Minute))
	injected := typedAt("p-peer", "<cross-session-message from=\"x\">use the peer machine for this</cross-session-message>", now.Add(-time.Hour))
	meta := typedAt("p-meta", "use the meta machine for this", now.Add(-time.Hour))
	meta["isMeta"] = true
	pasted := typedAt("p-paste", "look <pasted_content>use the pasted machine for this</pasted_content>", now.Add(-time.Hour))
	m.transcript(lead, old, injected, meta, pasted, typedAt("p-owner", "i want to try running it on m2max", now.Add(-2*time.Hour)))
	m.restart()
	m.session("claude", lead, leadSID)
	m.session("pi", "caller", otherSID)

	src, ok := m.d.findOwnerWords(t.Context(), otherSID, "I want to try running it on M2Max")
	if !ok || src.session != leadSID {
		t.Fatalf("the owner's words typed before the restart were not found: %+v %v", src, ok)
	}
	for _, quote := range []string{"use the old machine", "use the peer machine", "use the meta machine", "use the pasted machine"} {
		if _, ok := m.d.findOwnerWords(t.Context(), otherSID, quote); ok {
			t.Errorf("%q counted as the owner's words", quote)
		}
	}
	if !m.sentToGateway(leadSID, "i want to try running it on m2max") {
		t.Fatal("the rebuilt words were not sent to the gateway")
	}
}

// A reconnect sends every prompt the daemon keeps to the gateway again, so a
// gateway that restarted has them back.
func TestOwnerWordsAreSentToTheGatewayAgainOnReconnect(t *testing.T) {
	m := newRebuildMachine(t)
	// The hook and a transcript read can both keep one prompt: it is kept once.
	m.d.rememberOwnerPrompt(leadSID, "i want to try running it on m2max")
	m.d.rememberOwnerPrompt(leadSID, "i want to try running it on m2max")
	if n := len(m.d.ownerPrompts.by[leadSID]); n != 1 {
		t.Fatalf("one prompt kept %d times", n)
	}
	m.d.bg.Wait()
	m.mu.Lock()
	m.kept = nil
	m.mu.Unlock()
	m.d.refillOwnerWords(t.Context())
	if !m.sentToGateway(leadSID, "i want to try running it on m2max") {
		t.Fatal("the kept words were not sent to the gateway again")
	}
}
