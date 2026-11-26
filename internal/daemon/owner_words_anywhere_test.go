package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// overrideStartOnWords starts the override request from the caller with the
// quote and no reason.
func overrideStartOnWords(t *testing.T, pi *localClient, quote string) protocol.WorkerStartResult {
	t.Helper()
	req := overrideRequest()
	req.Reason = ""
	req.OwnerWords = quote
	return startFrom(t, pi, req)
}

// wantStartOnWords checks that a start went ahead at once on the owner's words
// typed in the named session, with no ask on the Desk.
func wantStartOnWords(t *testing.T, g *testGateway, res protocol.WorkerStartResult, typedIn string) {
	t.Helper()
	if res.SelectionPath != protocol.SelectionOwnerWords || res.SessionID == "" || res.Model != "fake/m2" {
		t.Fatalf("the start did not go ahead on the owner's words: %+v", res)
	}
	if n := len(overrideAsks(g)); n != 0 {
		t.Fatalf("%d asks for a start the owner asked for", n)
	}
	want := "owner_words found in what the owner typed in " + typedIn + " at about "
	if !strings.Contains(res.OwnerLine, want) {
		t.Fatalf("owner line %q does not say %q", res.OwnerLine, want)
	}
}

// The owner typed his words to the CTO, not in the caller's session. They
// still count: the start runs at once and names the CTO's session.
func TestOverrideOwnerWordsTypedInTheCTOChat(t *testing.T) {
	g, w, pi, _ := overrideNode(t)
	ctoOwnerSays(t, w.d, "Start the uadd3 work with a Codex  worker on M2Max, now")
	res := overrideStartOnWords(t, pi, "codex worker on m2max")
	wantStartOnWords(t, g, res, w.d.sessionName(context.Background(), w.d.ctoSessionID()))
}

// The owner typed his words in a session on another machine. The gateway's
// store of his words finds them.
func TestOverrideOwnerWordsTypedOnAnotherMachine(t *testing.T) {
	g, w, pi, _ := overrideNode(t)
	other := startNode(t, g, nodeOpts{cmdline: "pi"})
	_, leadID := helloPi(t, other, "lead", newRepo(t, "lab"), true)
	other.d.rememberOwnerPrompt(leadID, "please start a Codex worker on M2Max for this")
	eventually(t, "the gateway keeps the owner's words", func() bool {
		found, err := g.gw.Store().FindOwnerWords("codex worker on m2max")
		return err == nil && found.Found
	})
	res := overrideStartOnWords(t, pi, "codex worker on m2max")
	wantStartOnWords(t, g, res, w.d.sessionName(context.Background(), leadID))
}

// Words typed four hours ago on another machine are out of the window, and a
// quote found nowhere is held with one ask, as before.
func TestOverrideOwnerWordsOldOrNowhereIsHeld(t *testing.T) {
	g, w, pi, _ := overrideNode(t)
	other := startNode(t, g, nodeOpts{cmdline: "pi"})
	_, leadID := helloPi(t, other, "lead", newRepo(t, "lab"), true)
	other.d.rememberOwnerPromptAt(leadID, "please start a Codex worker on M2Max for this", time.Now().Add(-4*time.Hour))
	ctoOwnerSays(t, w.d, "fix the login page first")
	for i, quote := range []string{"codex worker on m2max", "an opus worker on the mac"} {
		req := overrideRequest()
		req.Title = "Held " + string(rune('A'+i))
		req.OwnerWords = quote
		res := startFrom(t, pi, req)
		if res.Status != "accepted" || res.SelectionPath != protocol.SelectionHeld || strings.Contains(res.OwnerLine, "owner_words found") {
			t.Fatalf("quote %q went ahead: %+v", quote, res)
		}
		eventually(t, "one ask per held start", func() bool { return len(overrideAsks(g)) == i+1 })
	}
}
