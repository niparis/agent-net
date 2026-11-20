package daemon

import (
	"context"
	"encoding/json"
	"strings"
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

// op runs one owner operation as the CTO and answers the notice's undo call.
func (r *noteCopyRig) op(t *testing.T, method string, args map[string]any) (undoMethod string, undoArgs json.RawMessage) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"area": "operator", "method": method, "arguments": args})
	if _, err := r.n.d.runCTOTool(r.ctx, "settings_change", raw); err != nil {
		t.Fatal(err)
	}
	items, err := inboxAndLog(r.g.gw.Store())
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if strings.Contains(it.Text, "CTO applied "+method) && len(it.Options) == 2 {
			undoMethod, undoArgs = it.Options[0], json.RawMessage(it.Options[1])
		}
	}
	if undoMethod == "" {
		t.Fatalf("no undo notice for %s: %+v", method, items)
	}
	return undoMethod, undoArgs
}

func TestCTOSessionAgentSetCopiesNotesOnlyWithTheFlagAndUndoRemovesThem(t *testing.T) {
	r := newNoteCopyRig(t)
	cli := cliClient(t, r.n)
	cli.mustCall("session_agent_set", map[string]any{"session_id": r.id, "agent": "helper"})
	if got := r.agentNotes(t, r.id); got != 0 {
		t.Fatalf("without copy_notes the moved session sees %d notes", got)
	}
	cli.mustCall("session_agent_set", map[string]any{"session_id": r.id, "agent": "demo"})
	method, args := r.op(t, "session_agent_set", map[string]any{"session_id": r.id, "agent": "helper", "copy_notes": true})
	if got := r.agentNotes(t, r.id); got != 2 {
		t.Fatalf("with copy_notes the moved session sees %d notes", got)
	}
	if got := r.agentNotes(t, r.lead); got != 2 {
		t.Fatalf("the old agent keeps its notes, sees %d", got)
	}
	cli.mustCall(method, args)
	if got := r.agentNotes(t, r.id); got != 2 {
		t.Fatalf("after undo the session is on demo again and sees %d", got)
	}
	view, ok, err := r.g.gw.Store().Agent(r.id)
	if err != nil || !ok || view.AgentID != "demo" {
		t.Fatalf("undo must move the session back: %+v %v", view, err)
	}
	helper, err := r.g.gw.Store().FindNotes(r.id, protocol.NotesFindRequest{Scope: "agent"})
	if err != nil || helper.Total != 2 {
		t.Fatalf("demo notes after undo: %+v %v", helper, err)
	}
}

func TestCTONotesCopyCopiesBetweenAgentsAndUndoRemovesOnlyTheCopies(t *testing.T) {
	r := newNoteCopyRig(t)
	cli := cliClient(t, r.n)
	cli.mustCall("session_agent_set", map[string]any{"session_id": r.id, "agent": "helper"})
	if _, err := r.g.gw.Store().WriteNote(r.id, protocol.NoteWriteRequest{Name: "alpha", Description: "d", Body: "helper own", Scope: "agent"}); err != nil {
		t.Fatal(err)
	}
	method, args := r.op(t, "notes_copy", map[string]any{"from_agent": "demo", "to_agent": "helper"})
	if got := r.agentNotes(t, r.id); got != 2 {
		t.Fatalf("helper sees %d notes, want its own alpha and the copied beta", got)
	}
	own, err := r.g.gw.Store().FindNotes(r.id, protocol.NotesFindRequest{Text: "helper own"})
	if err != nil || own.Total != 1 {
		t.Fatalf("the copy overwrote the agent's own note: %+v %v", own, err)
	}
	cli.mustCall(method, args)
	if got := r.agentNotes(t, r.id); got != 1 {
		t.Fatalf("undo left helper with %d notes, want its own alpha only", got)
	}
	if got := r.agentNotes(t, r.lead); got != 2 {
		t.Fatalf("demo kept %d notes", got)
	}
}
