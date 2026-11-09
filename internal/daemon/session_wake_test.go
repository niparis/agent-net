package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

const wakeNative = "11111111-0000-4000-8000-000000000000"

type wakeRig struct {
	c                        *claudeNode
	g                        *testGateway
	id, repo, listing, calls string
}

func newWakeRig(t *testing.T, state string) wakeRig {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "shop")
	listing, calls := filepath.Join(t.TempDir(), "agents.json"), filepath.Join(t.TempDir(), "calls")
	b, _ := json.Marshal([]map[string]string{{"id": wakeNative[:8], "cwd": repo, "state": state}})
	if err := os.WriteFile(listing, b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_AGENTS_FILE", listing)
	t.Setenv("FAKE_CLAUDE_AGENTS_LOG", calls)
	id, err := g.gw.Store().UpsertSession(c.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: wakeNative, Cwd: repo, Name: "shop", Mode: protocol.ModeBackground})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(c.d.NodeID(), id); err != nil {
		t.Fatal(err)
	}
	return wakeRig{c, g, id, repo, listing, calls}
}
func (r wakeRig) send(t *testing.T, body string) {
	t.Helper()
	_, err := r.c.d.call(context.Background(), "msg.send", protocol.MsgSend{Sender: r.c.ownerID(), Recipient: r.id, Body: body})
	if err != nil {
		t.Fatal(err)
	}
}
func (r wakeRig) listCalls() int {
	b, _ := os.ReadFile(r.calls)
	return strings.Count(string(b), "agents --json --all")
}

func TestMessageWakesRetiredClaude(t *testing.T) {
	r := newWakeRig(t, "done")
	defer func() {
		if t.Failed() {
			record, _ := os.ReadFile(filepath.Join(r.repo, "fake-claude.json"))
			t.Logf("listing calls=%d record=%s gateway=%v daemon=%v agents=%+v delivered=%+v", r.listCalls(), record, r.g.logs.lines(), r.c.logs.lines(), r.c.agents(t, true), r.c.adapter.deliveredTo(r.id))
		}
	}()
	r.send(t, "wake and deliver")
	eventually(t, "retired session receives message", func() bool {
		if lines := r.g.logs.lines("session wake", r.id); len(lines) > 0 {
			t.Fatalf("wake failed: %v", lines)
		}
		return len(r.c.adapter.deliveredTo(r.id)) == 1
	})
	rec := readClaude(t, r.repo)
	if argValue(rec.Args, "--resume") != wakeNative || r.listCalls() != 1 {
		t.Fatalf("resume args %v, listing calls %d", rec.Args, r.listCalls())
	}
}
func TestMessageWakesStoppedClaude(t *testing.T) {
	r := newWakeRig(t, "stopped")
	r.send(t, "wake stopped session")
	eventually(t, "stopped session receives message", func() bool {
		if lines := r.g.logs.lines("session wake", r.id); len(lines) > 0 {
			t.Fatalf("wake failed: %v", lines)
		}
		return len(r.c.adapter.deliveredTo(r.id)) == 1
	})
	rec := readClaude(t, r.repo)
	if argValue(rec.Args, "--resume") != wakeNative || r.listCalls() != 1 {
		t.Fatalf("resume args %v, listing calls %d", rec.Args, r.listCalls())
	}
}
func TestMessageDoesNotWakeWorkingClaude(t *testing.T) {
	r := newWakeRig(t, "working")
	r.send(t, "leave working")
	eventually(t, "working wake refused", func() bool { return r.listCalls() == 1 && len(r.g.logs.lines("session wake", r.id)) == 1 })
	if _, err := os.Stat(filepath.Join(r.repo, "fake-claude.json")); !os.IsNotExist(err) {
		t.Fatalf("working session was started: %v", err)
	}
}
func TestMessageWakeBackoff(t *testing.T) {
	r := newWakeRig(t, "done")
	t.Setenv(envFakeStartFails, "1")
	r.send(t, "first")
	eventually(t, "failed wake recorded", func() bool { return r.listCalls() == 1 && len(r.g.logs.lines("session wake", r.id)) == 1 })
	r.send(t, "second")
	r.send(t, "third")
	if n := r.listCalls(); n != 1 {
		t.Fatalf("background record checked %d times", n)
	}
}

func TestWakeRejectsUnsafeClaudeRecords(t *testing.T) {
	for _, kind := range []string{"removed", "invalid", "ambiguous", "two-stopped", "conflicting-id", "foreground", "foreground-no-kind", "live-done", "live-stopped", "working", "wrong-folder", "missing-folder", "worker"} {
		t.Run(kind, func(t *testing.T) {
			r := newWakeRig(t, "done")
			switch kind {
			case "removed":
				os.WriteFile(r.listing, []byte(`[]`), 0600)
			case "invalid":
				os.WriteFile(r.listing, []byte(`not json`), 0600)
			case "ambiguous":
				b, _ := json.Marshal([]map[string]string{{"id": wakeNative[:8], "cwd": r.repo, "state": "done"}, {"id": wakeNative[:8], "cwd": "/different", "state": "stopped"}})
				os.WriteFile(r.listing, b, 0600)
			case "two-stopped":
				b, _ := json.Marshal([]map[string]string{{"id": wakeNative[:8], "cwd": r.repo, "state": "stopped"}, {"id": wakeNative[:8], "cwd": r.repo, "state": "stopped"}})
				os.WriteFile(r.listing, b, 0600)
			case "conflicting-id":
				b, _ := json.Marshal([]map[string]string{{"id": wakeNative[:8], "sessionId": "different-native", "cwd": r.repo, "state": "done"}})
				os.WriteFile(r.listing, b, 0600)
			case "foreground":
				b, _ := json.Marshal([]map[string]string{{"id": wakeNative[:8], "kind": "interactive", "cwd": r.repo, "state": "done"}})
				os.WriteFile(r.listing, b, 0600)
			case "foreground-no-kind":
				_, err := r.g.gw.Store().UpsertSession(r.c.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: wakeNative, Cwd: r.repo, Mode: protocol.ModeForeground})
				if err != nil {
					t.Fatal(err)
				}
				if err := r.g.gw.Store().SetSessionOffline(r.c.d.NodeID(), r.id); err != nil {
					t.Fatal(err)
				}
			case "live-done":
				b, _ := json.Marshal(map[string]any{"id": wakeNative[:8], "kind": "background", "cwd": r.repo, "state": "done", "pid": 12345})
				os.WriteFile(r.listing, append(append([]byte("["), b...), ']'), 0600)
			case "live-stopped":
				b, _ := json.Marshal(map[string]any{"id": wakeNative[:8], "kind": "background", "cwd": r.repo, "state": "stopped", "pid": 12345})
				os.WriteFile(r.listing, append(append([]byte("["), b...), ']'), 0600)
			case "working":
				b, _ := json.Marshal([]map[string]string{{"id": wakeNative[:8], "cwd": r.repo, "state": "working"}})
				os.WriteFile(r.listing, b, 0600)
			case "wrong-folder":
				b, _ := json.Marshal([]map[string]string{{"id": wakeNative[:8], "cwd": "/different", "state": "done"}})
				os.WriteFile(r.listing, b, 0600)
			case "missing-folder":
				os.RemoveAll(r.repo)
			case "worker":
				_, err := r.g.gw.Store().UpsertSession(r.c.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: wakeNative, Cwd: r.repo, Worker: &protocol.WorkerInfo{ID: "w_test", Owner: r.c.ownerID()}})
				if err != nil {
					t.Fatal(err)
				}
				if err := r.g.gw.Store().SetSessionOffline(r.c.d.NodeID(), r.id); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.c.d.wakeRetiredClaude(context.Background(), r.id, ""); err == nil {
				t.Fatal("unsafe wake accepted")
			}
			if _, err := os.Stat(filepath.Join(r.repo, "fake-claude.json")); !os.IsNotExist(err) {
				t.Fatalf("unsafe session started: %v", err)
			}
		})
	}
}

func TestMessageWakesRetiredClaudeCopy(t *testing.T) {
	t.Setenv("FAKE_CLAUDE_NEW_RESUME_ID", "1")
	r := newWakeRig(t, "done")
	r.send(t, "wake copied conversation")
	eventually(t, "copy receives queued message under original session ID", func() bool { return len(r.c.adapter.deliveredTo(r.id)) == 1 })
	rec := readClaude(t, r.repo)
	if rec.Session == wakeNative {
		t.Fatal("fake did not copy the resumed session")
	}
}

func TestMessageWakesRetiredClaudeFullSessionID(t *testing.T) {
	r := newWakeRig(t, "done")
	b, _ := json.Marshal([]map[string]string{{"id": "abcd4321", "sessionId": wakeNative, "cwd": r.repo, "state": "done"}})
	if err := os.WriteFile(r.listing, b, 0600); err != nil {
		t.Fatal(err)
	}
	r.send(t, "wake by full session id")
	eventually(t, "full session ID is resumed", func() bool { return len(r.c.adapter.deliveredTo(r.id)) == 1 })
}

func TestMessageWakesRetiredClaudeBackgroundKind(t *testing.T) {
	r := newWakeRig(t, "done")
	b, _ := json.Marshal([]map[string]string{{"id": wakeNative[:8], "kind": "background", "cwd": r.repo, "state": "done"}})
	if err := os.WriteFile(r.listing, b, 0600); err != nil {
		t.Fatal(err)
	}
	r.send(t, "wake background")
	eventually(t, "background kind receives message", func() bool { return len(r.c.adapter.deliveredTo(r.id)) == 1 })
}
