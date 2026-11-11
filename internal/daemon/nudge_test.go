package daemon

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestNudgeForwardsCallerSession(t *testing.T) {
	n, f := startDeliveryNode(t)
	_, wt := deliveryWorktree(t)
	lc, sid := helloDelivery(t, n, "np-chief-nudge", wt, "gpt-6.1-sol")
	for _, tc := range []struct {
		op, frame string
		args      any
	}{
		{protocol.OpNudgePause, protocol.FrameNudgePause, protocol.NudgePauseArgs{Kind: "idle_check", Agent: "test", Mode: "preview"}},
		{protocol.OpNudgeResume, protocol.FrameNudgeResume, protocol.NudgeResumeArgs{ID: "np_test"}},
		{protocol.OpNudgePausesFind, protocol.FrameNudgePausesFind, protocol.NudgePausesFindArgs{}},
	} {
		f.answer(tc.frame, func(raw json.RawMessage) (any, error) { return protocol.NudgeResult{ID: "np_test"}, nil })
		if _, err := lc.call(tc.op, tc.args); err != nil {
			t.Fatal(err)
		}
		sent := f.frames(tc.frame)
		var q struct {
			SessionID string `json:"session_id"`
		}
		if len(sent) != 1 || json.Unmarshal(sent[0], &q) != nil || q.SessionID != sid {
			t.Fatalf("%s frames=%s want session=%s", tc.op, sent, sid)
		}
	}
}

// nudgeChiefRig starts a gateway and a node with one chief session and one
// target agent. It returns the node so a test can open a CLI connection too.
func nudgeChiefRig(t *testing.T) (*node, *testGateway, *localClient, string) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "/usr/bin/pi"})
	lc := dialLocal(t, n.sock)
	lc.AutoAck = true
	res := lc.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "nudge-chief",
		"cwd": "/w/chief", "status": "busy"})
	var hello struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(res, &hello); err != nil || hello.SessionID == "" {
		t.Fatalf("chief hello = %s: %v", res, err)
	}
	eventually(t, "chief online", func() bool {
		a, ok, err := g.gw.Store().Agent(hello.SessionID)
		return err == nil && ok && a.Online
	})
	if err := g.gw.Store().SetChief(protocol.ChiefSetRequest{SessionID: hello.SessionID}, "owner"); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(g.dir, "gateway.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO agents(id,display_name,created_at,updated_at) VALUES('target-agent','target-agent',0,0)`); err != nil {
		t.Fatal(err)
	}
	return n, g, lc, hello.SessionID
}

func nudgeChiefPause(t *testing.T, g *testGateway, chief string) (node, pauseID string) {
	t.Helper()
	n, err := g.gw.Store().SessionNode(chief)
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.gw.Store().PauseNudge(n, protocol.NudgePauseRequest{SessionID: chief,
		NudgePauseArgs: protocol.NudgePauseArgs{Kind: "idle_check", Agent: "target-agent", Mode: "apply"}})
	if err != nil {
		t.Fatal(err)
	}
	return n, out.ID
}

func nudgeEnded(t *testing.T, g *testGateway, node, chief, id string) bool {
	t.Helper()
	found, err := g.gw.Store().FindNudgePauses(node, chief)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range found.Pauses {
		if p.ID == id {
			return p.EndedAt != 0
		}
	}
	t.Fatalf("pause %s not found", id)
	return false
}

// The chief's agent_nudge_resume {id} goes through the daemon request encoding
// and ends the pause. The shipped test bypassed the encoder, so it missed the
// id clash.
func TestNudgeResumeThroughDaemonRequestPath(t *testing.T) {
	_, g, lc, chief := nudgeChiefRig(t)
	node, id := nudgeChiefPause(t, g, chief)
	raw := lc.mustCall(protocol.OpNudgeResume, protocol.NudgeResumeArgs{ID: id})
	var out protocol.NudgeResult
	if err := json.Unmarshal(raw, &out); err != nil || out.ID != id {
		t.Fatalf("resume answer = %s: %v", raw, err)
	}
	if !nudgeEnded(t, g, node, chief, id) {
		t.Fatal("pause still active after resume")
	}
}

// The owner's Desk Undo reads both the old {"id":...} and the new
// {"pause_id":...} option, and goes through the daemon request encoding.
func TestNudgeDeskUndoThroughDaemonRequestPath(t *testing.T) {
	undoOption := func(t *testing.T, g *testGateway, old bool) map[string]any {
		t.Helper()
		log, err := g.gw.Store().DeskLog(1)
		if err != nil || len(log) != 1 || len(log[0].Options) != 2 {
			t.Fatalf("desk log = %+v, %v", log, err)
		}
		var option map[string]any
		if err := json.Unmarshal([]byte(log[0].Options[1]), &option); err != nil {
			t.Fatal(err)
		}
		id := option["pause_id"]
		if id == nil {
			id = option["id"]
		}
		if id == nil || id == "" || option["receipt"] == nil {
			t.Fatalf("undo option = %v", option)
		}
		if old {
			return map[string]any{"id": id, "receipt": option["receipt"]}
		}
		return map[string]any{"pause_id": id, "receipt": option["receipt"]}
	}
	ownerUndo := func(t *testing.T, n *node, option map[string]any) {
		t.Helper()
		cli := dialLocal(t, n.sock)
		cli.mustCall("hello", map[string]any{"client": "cli"})
		cli.mustCall(protocol.OpNudgeUndo, option)
	}
	t.Run("old pause option ends the pause", func(t *testing.T) {
		n, g, _, chief := nudgeChiefRig(t)
		node, id := nudgeChiefPause(t, g, chief)
		ownerUndo(t, n, undoOption(t, g, true))
		if !nudgeEnded(t, g, node, chief, id) {
			t.Fatal("pause still active after old-form undo")
		}
	})
	t.Run("new pause option ends the pause", func(t *testing.T) {
		n, g, _, chief := nudgeChiefRig(t)
		node, id := nudgeChiefPause(t, g, chief)
		ownerUndo(t, n, undoOption(t, g, false))
		if !nudgeEnded(t, g, node, chief, id) {
			t.Fatal("pause still active after new-form undo")
		}
	})
	t.Run("new resume option restores the pause", func(t *testing.T) {
		n, g, lc, chief := nudgeChiefRig(t)
		node, id := nudgeChiefPause(t, g, chief)
		lc.mustCall(protocol.OpNudgeResume, protocol.NudgeResumeArgs{ID: id})
		if !nudgeEnded(t, g, node, chief, id) {
			t.Fatal("pause still active after resume")
		}
		ownerUndo(t, n, undoOption(t, g, false))
		if nudgeEnded(t, g, node, chief, id) {
			t.Fatal("pause still ended after undo of the resume")
		}
	})
}
