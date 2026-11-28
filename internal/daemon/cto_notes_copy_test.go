package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// noteCopyRig is a node with two lasting sessions in demo and an agent named
// helper. The demo agent has two agent-scope notes.
type noteCopyRig struct {
	g        *testGateway
	n        *node
	ctx      context.Context
	lead, id string
}

func newNoteCopyRig(t *testing.T) *noteCopyRig {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	fa := &fakeAdapter{harness: "fake"}
	n := startNode(t, g, nodeOpts{adapter: fa, cmdline: "pi"})
	ctx := context.Background()
	if err := n.d.registerCTOSession(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := g.gw.Store().AddProjects(n.d.NodeID(), protocol.ProjectAdd{Projects: []protocol.ProjectEntry{{Name: "demo", Path: "/work/demo"}}}); err != nil {
		t.Fatal(err)
	}
	if err := g.gw.Store().AddArchitect("helper", "owner"); err != nil {
		t.Fatal(err)
	}
	lead := fakeSession("lead", "lead")
	lead.Cwd = "/work/demo"
	lead.Repo = protocol.RepoInfo{Project: "demo", Root: "/work/demo"}
	sub := fakeSession("sub", "sub")
	sub.Cwd, sub.Repo = lead.Cwd, lead.Repo
	fa.setSessions(lead, sub)
	eventually(t, "sessions known", func() bool {
		return fa.host.SessionID("fake", "lead") != "" && fa.host.SessionID("fake", "sub") != ""
	})
	r := &noteCopyRig{g: g, n: n, ctx: ctx, lead: fa.host.SessionID("fake", "lead"), id: fa.host.SessionID("fake", "sub")}
	for _, name := range []string{"alpha", "beta"} {
		if _, err := g.gw.Store().WriteNote(r.lead, protocol.NoteWriteRequest{Name: name, Description: "d", Body: "demo " + name, Scope: "agent"}); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *noteCopyRig) agentNotes(t *testing.T, session string) int {
	t.Helper()
	res, err := r.g.gw.Store().FindNotes(session, protocol.NotesFindRequest{Scope: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	return res.Total
}

// op runs one owner operation as the CTO.
func (r *noteCopyRig) op(t *testing.T, method string, args map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"area": "operator", "method": method, "arguments": args})
	if _, err := r.n.d.runCTOTool(r.ctx, "settings_change", raw); err != nil {
		t.Fatal(err)
	}
}

func TestCTOSessionAgentSetCopiesNotesOnlyWithTheFlagAndUndoRemovesThem(t *testing.T) {
	r := newNoteCopyRig(t)
	cli := cliClient(t, r.n)
	cli.mustCall("session_agent_set", map[string]any{"session_id": r.id, "agent": "helper"})
	if got := r.agentNotes(t, r.id); got != 0 {
		t.Fatalf("without copy_notes the moved session sees %d notes", got)
	}
	cli.mustCall("session_agent_set", map[string]any{"session_id": r.id, "agent": "demo"})
	r.op(t, "session_agent_set", map[string]any{"session_id": r.id, "agent": "helper", "copy_notes": true})
	if got := r.agentNotes(t, r.id); got != 2 {
		t.Fatalf("with copy_notes the moved session sees %d notes", got)
	}
	if got := r.agentNotes(t, r.lead); got != 2 {
		t.Fatalf("the old agent keeps its notes, sees %d", got)
	}
	cli.mustCall("session_agent_set", map[string]any{"session_id": r.id, "agent": "demo"})
	view, ok, err := r.g.gw.Store().Agent(r.id)
	if err != nil || !ok || view.AgentID != "demo" {
		t.Fatalf("move back must restore demo: %+v %v", view, err)
	}
	helper, err := r.g.gw.Store().FindNotes(r.id, protocol.NotesFindRequest{Scope: "agent"})
	if err != nil || helper.Total != 2 {
		t.Fatalf("demo notes after move back: %+v %v", helper, err)
	}
}

func TestCTONotesCopyCopiesBetweenAgentsAndUndoRemovesOnlyTheCopies(t *testing.T) {
	r := newNoteCopyRig(t)
	cli := cliClient(t, r.n)
	cli.mustCall("session_agent_set", map[string]any{"session_id": r.id, "agent": "helper"})
	if _, err := r.g.gw.Store().WriteNote(r.id, protocol.NoteWriteRequest{Name: "alpha", Description: "d", Body: "helper own", Scope: "agent"}); err != nil {
		t.Fatal(err)
	}
	r.op(t, "notes_copy", map[string]any{"from_agent": "demo", "to_agent": "helper"})
	if got := r.agentNotes(t, r.id); got != 2 {
		t.Fatalf("helper sees %d notes, want its own alpha and the copied beta", got)
	}
	own, err := r.g.gw.Store().FindNotes(r.id, protocol.NotesFindRequest{Text: "helper own"})
	if err != nil || own.Total != 1 {
		t.Fatalf("the copy overwrote the agent's own note: %+v %v", own, err)
	}
	if got := r.agentNotes(t, r.lead); got != 2 {
		t.Fatalf("demo kept %d notes", got)
	}
}
