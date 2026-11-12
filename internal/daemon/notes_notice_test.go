package daemon

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A Claude Code session keeps every hook answer in its transcript. The notes
// index goes in once, at the start. A change to the notes then sends a short
// notice with the changed titles, and never the charter again (decision 0158).
func TestClaudeNotesIndexOnceThenShortNoticeOnChange(t *testing.T) {
	t.Parallel()
	g, n, cli, nativeID := claudeCharterSession(t)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	n.d.policy.now = func() time.Time { return now }
	n.d.mu.Lock()
	ls := n.d.sessions[key("claude", nativeID)]
	n.d.mu.Unlock()
	sid := n.d.currentSessionID(ls)
	st := g.gw.Store()
	write := func(name, desc, body string) {
		t.Helper()
		if _, err := st.WriteNote(sid, protocol.NoteWriteRequest{Name: name, Description: desc, Body: body, Scope: "project"}); err != nil {
			t.Fatal(err)
		}
	}
	next := func(prompt string) protocol.PolicyResult {
		t.Helper()
		now = now.Add(time.Minute)
		return claudePrompt(t, cli, nativeID, prompt)
	}
	write("deploy-order", "what ships first", "gateway before nodes")

	first := claudePrompt(t, cli, nativeID, "first")
	if !strings.Contains(first.Charter, "maturity mature") || !strings.Contains(first.Charter, "- deploy-order (fact): what ships first") {
		t.Fatalf("the first prompt must carry the charter and the notes index:\n%s", first.Charter)
	}
	if got := next("second"); got.Charter != "" {
		t.Fatalf("nothing changed, yet the prompt carried:\n%s", got.Charter)
	}

	write("release-flow", "how a release ships", "merger tags")
	changed := next("third")
	if strings.Contains(changed.Charter, "maturity") || strings.Contains(changed.Charter, protocol.CharterChangedNotice) {
		t.Fatalf("a note change sent the charter again:\n%s", changed.Charter)
	}
	if !strings.Contains(changed.Charter, "Your notes changed") || !strings.Contains(changed.Charter, "- release-flow (fact): how a release ships") {
		t.Fatalf("a note change must send a notice with the changed title:\n%s", changed.Charter)
	}
	if strings.Contains(changed.Charter, "deploy-order") {
		t.Fatalf("the notice lists a title that did not change:\n%s", changed.Charter)
	}
	t.Logf("first send %d bytes (charter and index); a note change now sends %d bytes (notice only)", len(first.Charter), len(changed.Charter))
	if len(changed.Charter) >= len(first.Charter)/2 {
		t.Fatalf("the notice is not short: %d bytes against %d", len(changed.Charter), len(first.Charter))
	}

	// A new body with the same title changes nothing the session was told.
	write("release-flow", "how a release ships", "merger tags and publishes")
	if got := next("fourth"); got.Charter != "" {
		t.Fatalf("a body-only update sent:\n%s", got.Charter)
	}
	// A new description is a changed title.
	write("release-flow", "how a release ships and who checks it", "merger tags and publishes")
	if got := next("fifth"); !strings.Contains(got.Charter, "how a release ships and who checks it") || strings.Contains(got.Charter, "maturity") {
		t.Fatalf("a changed description must be in the notice, without the charter:\n%s", got.Charter)
	}
	// A note that leaves the list is named too.
	found, err := st.FindNotes(sid, protocol.NotesFindRequest{Text: "deploy-order"})
	if err != nil || found.Total != 1 {
		t.Fatalf("find the note to remove: %+v %v", found, err)
	}
	if _, err := st.RemoveStandingOrder(found.Notes[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := next("sixth"); !strings.Contains(got.Charter, "deploy-order") || !strings.Contains(got.Charter, "no longer") {
		t.Fatalf("the notice must name the removed note:\n%s", got.Charter)
	}
	if got := next("seventh"); got.Charter != "" {
		t.Fatalf("the notice was repeated:\n%s", got.Charter)
	}
	// After a compaction the summary may have dropped the index: send it whole.
	cli.mustCall(protocol.MethodCompaction, protocol.CompactionRequest{Harness: "claude", NativeID: nativeID})
	again := next("eighth")
	if !strings.Contains(again.Charter, "- release-flow (fact)") || !strings.Contains(again.Charter, "maturity mature") {
		t.Fatalf("after a compaction the charter and the index must come again:\n%s", again.Charter)
	}
}
