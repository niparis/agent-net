package gateway

import (
	"sort"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// copyRig is a store with the sessions of two projects and the owner session of
// their node. Project demo's session has agent "demo".
func copyRig(t *testing.T) (s *Store, node, owner, demo, other string) {
	t.Helper()
	s, _ = newTestStore(t)
	ids, node := graphSessions(t, s, "demo", "other")
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddArchitect("lead", "owner"); err != nil {
		t.Fatal(err)
	}
	return s, node, owner, ids["demo"], ids["other"]
}

func agentNoteNames(t *testing.T, s *Store, session string) []string {
	t.Helper()
	res, err := s.FindNotes(session, protocol.NotesFindRequest{Scope: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, n := range res.Notes {
		names = append(names, n.Name)
	}
	sort.Strings(names)
	return names
}

func TestCopyAgentNotesGivesTheNewAgentTheNotesAndKeepsTheOld(t *testing.T) {
	s, node, owner, demo, other := copyRig(t)
	for _, n := range []string{"alpha", "beta", "shared"} {
		noteWrite(t, s, demo, protocol.NoteWriteRequest{Name: n, Description: "d", Body: "demo " + n, Scope: "agent"})
	}
	// The other session joins agent lead, which already has a note named shared.
	if _, err := s.setSessionAgent(node, protocol.SessionAgentSetRequest{Owner: owner, SessionID: other, Agent: "lead"}); err != nil {
		t.Fatal(err)
	}
	noteWrite(t, s, other, protocol.NoteWriteRequest{Name: "shared", Description: "d", Body: "keep me", Scope: "agent"})

	res, err := s.CopyAgentNotes(node, protocol.NotesCopyRequest{Owner: owner, FromAgent: "demo", ToAgent: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.CopiedNotes) != 2 || len(res.KeptNotes) != 1 || res.KeptNotes[0] != "shared" {
		t.Fatalf("result %+v", res)
	}
	if got := strings.Join(agentNoteNames(t, s, other), ","); got != "alpha,beta,shared" {
		t.Fatalf("the new agent sees %s", got)
	}
	if got := strings.Join(agentNoteNames(t, s, demo), ","); got != "alpha,beta,shared" {
		t.Fatalf("the old agent must keep its notes, sees %s", got)
	}
	found, err := s.FindNotes(other, protocol.NotesFindRequest{Text: "keep"})
	if err != nil || found.Total != 1 || found.Notes[0].Body != "keep me" {
		t.Fatalf("an existing note must not be overwritten: %+v %v", found, err)
	}
	// Copying again adds nothing: every name is there.
	again, err := s.CopyAgentNotes(node, protocol.NotesCopyRequest{Owner: owner, FromAgent: "demo", ToAgent: "lead"})
	if err != nil || len(again.CopiedNotes) != 0 || len(again.KeptNotes) != 3 {
		t.Fatalf("second copy %+v %v", again, err)
	}
}

func TestUncopyNotesRemovesOnlyTheCopies(t *testing.T) {
	s, node, owner, demo, other := copyRig(t)
	noteWrite(t, s, demo, protocol.NoteWriteRequest{Name: "alpha", Description: "d", Body: "demo alpha", Scope: "agent"})
	if _, err := s.setSessionAgent(node, protocol.SessionAgentSetRequest{Owner: owner, SessionID: other, Agent: "lead"}); err != nil {
		t.Fatal(err)
	}
	noteWrite(t, s, other, protocol.NoteWriteRequest{Name: "own", Description: "d", Body: "lead own", Scope: "agent"})
	res, err := s.CopyAgentNotes(node, protocol.NotesCopyRequest{Owner: owner, FromAgent: "demo", ToAgent: "lead"})
	if err != nil || len(res.CopiedNotes) != 1 {
		t.Fatalf("copy %+v %v", res, err)
	}
	if err := s.UncopyNotes(node, protocol.NotesUncopyRequest{Owner: owner, IDs: res.CopiedNotes}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(agentNoteNames(t, s, other), ","); got != "own" {
		t.Fatalf("the new agent sees %s", got)
	}
	if got := strings.Join(agentNoteNames(t, s, demo), ","); got != "alpha" {
		t.Fatalf("the old agent must keep its notes, sees %s", got)
	}
}

func TestCopyAgentNotesIsForTheOwnerAndNamesRealAgents(t *testing.T) {
	s, node, owner, demo, _ := copyRig(t)
	if _, err := s.CopyAgentNotes(node, protocol.NotesCopyRequest{Owner: demo, FromAgent: "demo", ToAgent: "lead"}); err == nil {
		t.Fatal("a non-owner session copied notes")
	}
	if _, err := s.CopyAgentNotes(node, protocol.NotesCopyRequest{Owner: owner, FromAgent: "demo", ToAgent: "nobody"}); err == nil {
		t.Fatal("copied to an unknown agent")
	}
	if _, err := s.CopyAgentNotes(node, protocol.NotesCopyRequest{Owner: owner, FromAgent: "demo", ToAgent: "demo"}); err == nil {
		t.Fatal("copied an agent onto itself")
	}
}

func TestSessionAgentSetCopiesNotesOnlyWhenAsked(t *testing.T) {
	s, node, owner, demo, other := copyRig(t)
	noteWrite(t, s, demo, protocol.NoteWriteRequest{Name: "alpha", Description: "d", Body: "demo alpha", Scope: "agent"})
	if err := s.AddArchitect("plain", "owner"); err != nil {
		t.Fatal(err)
	}
	// Without the flag the moved session sees no old notes, as before.
	if _, err := s.setSessionAgent(node, protocol.SessionAgentSetRequest{Owner: owner, SessionID: demo, Agent: "plain"}); err != nil {
		t.Fatal(err)
	}
	if got := agentNoteNames(t, s, demo); len(got) != 0 {
		t.Fatalf("without copy_notes the session sees %v", got)
	}
	// Back on its old agent, then to lead with the flag.
	if _, err := s.setSessionAgent(node, protocol.SessionAgentSetRequest{Owner: owner, SessionID: demo, Agent: "demo"}); err != nil {
		t.Fatal(err)
	}
	_ = other
	res, err := s.setSessionAgent(node, protocol.SessionAgentSetRequest{Owner: owner, SessionID: demo, Agent: "lead", CopyNotes: true})
	if err != nil || !res.OK || len(res.CopiedNotes) != 1 {
		t.Fatalf("result %+v %v", res, err)
	}
	if got := strings.Join(agentNoteNames(t, s, demo), ","); got != "alpha" {
		t.Fatalf("with copy_notes the session sees %s", got)
	}
}
