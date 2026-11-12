package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session writes a note with agent_note_write, finds it with
// agent_notes_find, and sees its title (not its body) in the charter text the
// gateway sends (decision 0158). The command line has no notes.
func TestAgentNotesWriteFindAndCharterIndex(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	client, id := helloDelivery(t, n, "caller", repo, "claude-opus-4")
	eventually(t, "session and repo registered", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && a.Repo.Project == "demo"
	})
	if err := g.gw.Store().SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}

	cli := cliClient(t, n)
	if _, err := cli.call(protocol.OpNoteWrite, protocol.NoteWriteRequest{Name: "x", Description: "d", Body: "b"}); err == nil {
		t.Fatal("the command line wrote a note")
	}

	var w protocol.NoteWriteResult
	mustJSON(t, client.mustCall(protocol.OpNoteWrite, protocol.NoteWriteRequest{Name: "Deploy Order", Description: "what ships first", Body: "SECRET-BODY ship the gateway before the nodes", Scope: "project", Kind: "fact"}), &w)
	if !w.Created || w.Name != "deploy-order" {
		t.Fatalf("write: %+v", w)
	}
	var f protocol.NotesFindResult
	mustJSON(t, client.mustCall(protocol.OpNotesFind, protocol.NotesFindRequest{Text: "gateway"}), &f)
	if f.Total != 1 || !strings.Contains(f.Notes[0].Body, "ship the gateway") {
		t.Fatalf("find: %+v", f)
	}

	got := askPolicy(t, client, protocol.PolicyRequest{})
	if !strings.Contains(got.Charter, "- deploy-order (fact): what ships first") || strings.Contains(got.Charter, "SECRET-BODY") {
		t.Fatalf("the charter text should carry the title and not the body:\n%s", got.Charter)
	}
}

// The CTO has no session folder, so its notes are those of the agent "cto" and
// the owner-wide ones, through its own MCP tool path.
func TestCTOWritesAndFindsNotes(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startWorkerNode(t, g, shortSet, nil)
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	out, err := n.d.runCTOToolRaw(context.Background(), "agent_note_write", json.RawMessage(`{"name":"owner-taste","description":"how the owner likes reports","body":"short, plain words","scope":"owner","kind":"feedback"}`))
	if err != nil {
		t.Fatalf("CTO write: %v", err)
	}
	b, _ := json.Marshal(out)
	var w protocol.NoteWriteResult
	if err := json.Unmarshal(b, &w); err != nil || !w.Created {
		t.Fatalf("write result: %s %v", b, err)
	}
	if _, err := n.d.runCTOToolRaw(context.Background(), "agent_note_write", json.RawMessage(`{"name":"my-habit","description":"cto habit","body":"check the desk first"}`)); err != nil {
		t.Fatalf("CTO agent-scope write: %v", err)
	}
	out, err = n.d.runCTOToolRaw(context.Background(), "agent_notes_find", json.RawMessage(`{"text":"plain"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(out)
	var f protocol.NotesFindResult
	if err := json.Unmarshal(b, &f); err != nil || f.Total != 1 || f.Notes[0].Name != "owner-taste" {
		t.Fatalf("find: %s %v", b, err)
	}
}
