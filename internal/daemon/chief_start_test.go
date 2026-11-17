package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// chiefStartNode is a Claude node whose state folder sits under HOME, which
// the one session start requires of a folder it creates.
func chiefStartNode(t *testing.T, g *testGateway) *claudeNode {
	t.Helper()
	n := startClaudeNode(t, g, shortSet)
	t.Setenv("HOME", filepath.Dir(n.d.cfg.StateDir))
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", n.sessions)
	return n
}

func setChiefModels(t *testing.T, g *testGateway, line string) {
	t.Helper()
	value, _ := json.Marshal(line)
	snap, err := g.gw.Store().SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	var stored json.RawMessage
	for _, v := range snap.Values {
		if v.Key == protocol.ChiefModelKey && v.Level == "network" {
			stored = v.Value
		}
	}
	if _, err := g.gw.Store().WriteSettings([]gateway.SettingsChange{{Key: protocol.ChiefModelKey, Level: "network", Scope: "network", Value: value, Expect: stored}}, "owner", "test"); err != nil {
		t.Fatal(err)
	}
}

func killRecordedClaude(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		raw, _ := os.ReadFile(filepath.Join(dir, "fake-claude.json"))
		var rec fakeClaudeRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Pgid > 1 {
			syscall.Kill(-rec.Pgid, syscall.SIGKILL)
		}
	})
}

// With no chief pinned, the chief starts through the ordinary session start on
// the first usable entry of chief.model, with the harness memory off, and the
// session it started is pinned (decision 0171, phase 2). A second look finds a
// chief that is online and starts nothing.
func TestChiefStartsFromItsModelListAndIsPinned(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := chiefStartNode(t, g)
	setChiefModels(t, g, "claude · anthropic · claude-opus-5-5")
	ctx := context.Background()
	if err := n.d.ensureChief(ctx); err != nil {
		t.Fatal(err)
	}
	dir := n.d.chiefFolder()
	killRecordedClaude(t, dir)
	st, err := g.gw.Store().ChiefStatus()
	if err != nil || st.SessionID == "" {
		t.Fatalf("chief status = %+v, err = %v, want the started session pinned", st, err)
	}
	a, ok, err := g.gw.Store().Agent(st.SessionID)
	if err != nil || !ok || a.Harness != "claude" || a.NodeID != n.d.NodeID() {
		t.Fatalf("chief session = %+v ok=%v err=%v", a, ok, err)
	}
	rec := readClaude(t, dir)
	if argValue(rec.Args, "--model") != "claude-opus-5-5" || rec.Args[len(rec.Args)-1] != chiefFirstMessage {
		t.Fatalf("args = %v", rec.Args)
	}
	if off, ok := rec.Settings["autoMemoryEnabled"].(bool); !ok || off {
		t.Fatalf("settings = %v, want autoMemoryEnabled false: the chief's memory is gateway notes", rec.Settings)
	}
	if err := n.d.ensureChief(ctx); err != nil {
		t.Fatal(err)
	}
	if again, _ := g.gw.Store().ChiefStatus(); again.SessionID != st.SessionID {
		t.Fatalf("a second look started another chief: %s, then %s", st.SessionID, again.SessionID)
	}
}

// A pinned chief that is offline on a harness no message can wake (Pi) is
// replaced by a started one. Messages sent to the old session reach the new
// chief, and Pi is never chosen from the list.
func TestOfflinePiChiefIsReplacedByAStartedChief(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := chiefStartNode(t, g)
	setChiefModels(t, g, "pi · pi · pi-model\nclaude · anthropic · claude-opus-5-5")
	_, oldChief := helloPi(t, n.node, "old-chief", "/w/chief", true)
	if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: oldChief}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(n.d.NodeID(), oldChief); err != nil {
		t.Fatal(err)
	}
	if err := n.d.ensureChief(context.Background()); err != nil {
		t.Fatal(err)
	}
	killRecordedClaude(t, n.d.chiefFolder())
	st, err := g.gw.Store().ChiefStatus()
	if err != nil || st.SessionID == "" || st.SessionID == oldChief {
		t.Fatalf("chief = %+v err = %v, want a new session instead of the offline Pi %s", st, err, oldChief)
	}
	a, _, err := g.gw.Store().Agent(oldChief)
	if err != nil || a.ReplacedBy != st.SessionID {
		t.Fatalf("old chief = %+v err = %v, want it replaced by %s", a, err, st.SessionID)
	}
}

// An offline chief on a harness a message can wake is left alone: the wake
// path brings it back, and a second chief would split the asks.
func TestOfflineWakeableChiefIsNotReplaced(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := chiefStartNode(t, g)
	setChiefModels(t, g, "claude · anthropic · claude-opus-5-5")
	session, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "codex", NativeID: "sleeping-chief", Cwd: "/w/chief", Name: "sleeping chief"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: session}, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetSessionOffline(n.d.NodeID(), session); err != nil {
		t.Fatal(err)
	}
	if err := n.d.ensureChief(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, _ := g.gw.Store().ChiefStatus(); st.SessionID != session {
		t.Fatalf("chief = %s, want the sleeping chief %s kept", st.SessionID, session)
	}
}

// A chief whose list has only Pi is refused with the reason, and nothing starts.
func TestChiefStartRefusesPiWithItsReason(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := chiefStartNode(t, g)
	setChiefModels(t, g, "pi · pi · pi-model")
	err := n.d.ensureChief(context.Background())
	if err == nil || err.Error() != "chief model: Pi cannot be woken by a message yet" {
		t.Fatalf("ensureChief = %v, want the Pi reason", err)
	}
	if st, _ := g.gw.Store().ChiefStatus(); st.SessionID != "" {
		t.Fatalf("a chief was pinned: %+v", st)
	}
}

// A resumed or woken chief keeps the launch settings of its start, so its
// harness memory stays off. Another session's resume does not get them.
func TestResumedChiefKeepsItsMemoryOff(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := chiefStartNode(t, g)
	chief, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: "chief-native", Cwd: "/w/chief", Name: "chief"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := g.gw.Store().UpsertSession(n.d.NodeID(), protocol.SessionInfo{Harness: "claude", NativeID: "other-native", Cwd: "/w/other", Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: chief}, "owner"); err != nil {
		t.Fatal(err)
	}
	if spec := n.d.claudeResumeSpec(protocol.AgentView{SessionID: chief, NativeID: "chief-native", Cwd: "/w/chief"}); !strings.Contains(spec.Settings, `"autoMemoryEnabled":false`) {
		t.Fatalf("chief resume settings = %q, want autoMemoryEnabled false", spec.Settings)
	}
	if spec := n.d.claudeResumeSpec(protocol.AgentView{SessionID: other, NativeID: "other-native", Cwd: "/w/other"}); strings.Contains(spec.Settings, "autoMemoryEnabled") {
		t.Fatalf("another session's resume settings = %q, want no memory setting", spec.Settings)
	}
}

// A chief the owner cleared on purpose is not started again by the loop. Naming
// a chief, or changing chief.model, ends the clear.
func TestChiefStartHonoursAClear(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := chiefStartNode(t, g)
	setChiefModels(t, g, "claude · anthropic · claude-opus-5-5")
	cli := cliClient(t, n.node)
	cli.mustCall("chief_set", map[string]any{"clear": true})
	ctx := context.Background()
	if err := n.d.ensureChief(ctx); err != nil {
		t.Fatal(err)
	}
	if st, _ := g.gw.Store().ChiefStatus(); st.SessionID != "" {
		t.Fatalf("ensureChief started a chief after a clear: %+v", st)
	}
	setChiefModels(t, g, "claude · anthropic · claude-opus-5-5\ncodex · openai · gpt-6.1-sol")
	if err := n.d.ensureChief(ctx); err != nil {
		t.Fatal(err)
	}
	killRecordedClaude(t, n.d.chiefFolder())
	if st, _ := g.gw.Store().ChiefStatus(); st.SessionID == "" {
		t.Fatal("a chief.model change did not end the clear")
	}
}
