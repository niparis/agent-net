package daemon

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestLearnedCTOSuggestionsIncludesReadyMajor(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, shortSet, nil)
	s := g.gw.Store()
	node, _, err := s.CreateNode("test", "linux")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "a", Repo: protocol.RepoInfo{Project: "app"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "b", Repo: protocol.RepoInfo{Project: "ops"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		m, _, e := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "task"})
		if e != nil {
			t.Fatal(e)
		}
		_, _, e = s.StoreMessage(protocol.MsgSend{Sender: b, Recipient: a, ParentID: m.ID, Body: "no", ReplyOutcome: protocol.ReplyOutcome{Outcome: "reject", Reason: "deploy changes"}})
		if e != nil {
			t.Fatal(e)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(g.dir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE learned_suggestions SET ready_at=0`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if err = s.ProcessLearned(); err != nil {
		t.Fatal(err)
	}
	if all, e := s.OwnerLearned(); e != nil || len(all) != 1 {
		t.Fatalf("owner learned %+v: %v", all, e)
	}
	if _, e := w.d.call(context.Background(), "learned.list", map[string]string{"owner": w.ownerID()}); e != nil {
		t.Fatalf("learned frame: %v", e)
	}
	raw, err := w.d.runCTOToolRaw(context.Background(), "charter_suggestions", nil)
	if err != nil {
		t.Fatal(err)
	}
	result := raw.(charterSuggestionResult)
	if len(result.PendingMajors) != 1 || result.PendingMajors[0].Field != "refuses" {
		t.Fatalf("result: %+v", result)
	}
}

func TestLearnedLocalMethodsAreOwnerOnly(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi", interval: time.Hour}, nil)
	agent, _ := helloPi(t, n, "learning-agent", "/w/learning", true)
	cli := cliClient(t, n)
	cli.mustCall("learned_list", nil)
	for _, method := range []string{"learned_list", "learned_answer", "learned_view"} {
		if _, err := agent.call(method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "only available to the owner") {
			t.Fatalf("agent %s gate: %v", method, err)
		}
	}
	n.d.inSession = func(int) bool { return true }
	if _, err := cli.call("learned_list", nil); err == nil || !strings.Contains(err.Error(), "inside an agent session") {
		t.Fatalf("in-session CLI gate: %v", err)
	}
}
