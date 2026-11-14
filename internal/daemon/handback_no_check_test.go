package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// setHandbackCheck writes the retired pre-hand-back check settings, so the
// test proves a configured project is not checked any more.
func setHandbackCheck(t *testing.T, g *testGateway, project, command, timeout string) {
	t.Helper()
	changes := []protocol.SettingsChange{{Key: "delivery.handback_check", Level: "repo", Scope: project, Value: mustRawJSON(command)}}
	if timeout != "" {
		changes = append(changes, protocol.SettingsChange{Key: "delivery.handback_check_timeout", Level: "repo", Scope: project, Value: mustRawJSON(timeout)})
	}
	if _, err := g.gw.Store().WriteSettings(changes, "owner", "test hand-back check"); err != nil {
		t.Fatal(err)
	}
}

func mustRawJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func noticeChangeID(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "change: ch_") {
			return strings.TrimSpace(strings.TrimPrefix(line, "change: "))
		}
	}
	t.Fatalf("notice names no change:\n%s", body)
	return ""
}

func changeView(t *testing.T, g *testGateway, id string) protocol.ChangeView {
	t.Helper()
	view, err := g.gw.Store().GetChange(id)
	if err != nil {
		t.Fatalf("change %s: %v", id, err)
	}
	return view
}

// A complete hand-back records its change at once. The daemon no longer runs
// the project's pre-hand-back check: the owner made the merge queue's CI the
// single test gate (decision 2026-11-14), because every hand-back waited in
// one serial queue for a check that repeated what CI does.
func TestHandbackRecordsChangeWithoutRunningTheCheck(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, queueSet, withLoad(&fakeLoad{l: roomyLoad()}))
	repo := newRepo(t, "shop")
	w.register(t, repo)
	ran := filepath.Join(t.TempDir(), "ran")
	setHandbackCheck(t, g, "shop", "echo ran > "+ran+"\nexit 1\n", "")

	w.mustStart(t, protocol.WorkerStartRequest{Project: "shop", Harness: "opencode", Model: "fake/m1",
		Task: "FAKE:handback FAKE:edit", Isolation: protocol.IsolationWorktree})
	n := w.waitNotice(t, 1)[0]
	if strings.Contains(n.Body, "hand-back check:") {
		t.Fatalf("the notice still carries a hand-back check line:\n%s", n.Body)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the daemon ran the retired hand-back check")
	}
	view := changeView(t, g, noticeChangeID(t, n.Body))
	if view.HandbackCheck != nil {
		t.Fatalf("the change carries a hand-back check: %+v", view.HandbackCheck)
	}
	if view.Change.HeadCommit == "" {
		t.Fatal("the hand-back recorded no change")
	}
}
