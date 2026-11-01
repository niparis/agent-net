package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The TUI reads the CTO box through session_text on the CTO session.
func TestCTOSessionTextReturnsTheTimeline(t *testing.T) {
	f := startCTOCompactionFixture(t)
	f.writeMessages(t, []map[string]any{
		{"type": "user", "text": "hello from the owner", "time": map[string]any{"created": 1000}},
		{"type": "assistant", "text": "hello back", "time": map[string]any{"created": 2000}},
	})
	if err := f.n.d.recordCTOAction("sent to peer: hello"); err != nil {
		t.Fatal(err)
	}
	var page protocol.TextPage
	if err := json.Unmarshal(f.cli.mustCall("session_text", protocol.TextQuery{SessionID: f.n.d.ctoSessionID(), Limit: 50}), &page); err != nil {
		t.Fatal(err)
	}
	if page.SessionID != f.n.d.ctoSessionID() || page.Harness != "cto" {
		t.Fatalf("page identity: %+v", page)
	}
	var roles, texts []string
	for _, e := range page.Entries {
		roles = append(roles, e.Role+"/"+e.Tool)
		texts = append(texts, e.Text)
	}
	want := []string{"user/", "assistant/", "assistant/cto_action"}
	if !reflect.DeepEqual(roles, want) {
		t.Fatalf("roles = %v, want %v (texts %v)", roles, want, texts)
	}
	if texts[0] != "hello from the owner" || texts[1] != "hello back" || texts[2] != "sent to peer: hello" {
		t.Fatalf("texts = %v", texts)
	}
}

func TestCTOSessionTextResolvesTheCTOName(t *testing.T) {
	f := startCTOCompactionFixture(t)
	var page protocol.TextPage
	if err := json.Unmarshal(f.cli.mustCall("session_text", protocol.TextQuery{SessionID: protocol.CTONodeName}), &page); err != nil {
		t.Fatal(err)
	}
	if page.SessionID != f.n.d.ctoSessionID() {
		t.Fatalf("the name CTO resolved to %q, want %q", page.SessionID, f.n.d.ctoSessionID())
	}
}

func TestCTOSessionEnterAndCTOEnterGiveTheSamePlan(t *testing.T) {
	f := startCTOCompactionFixture(t)
	var old, byID, byName protocol.EnterPlan
	if err := json.Unmarshal(f.cli.mustCall("cto_enter", nil), &old); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(f.cli.mustCall("session_enter", protocol.EnterRequest{SessionID: f.n.d.ctoSessionID()}), &byID); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(f.cli.mustCall("session_enter", protocol.EnterRequest{SessionID: protocol.CTONodeName}), &byName); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old.Argv, byID.Argv) || !reflect.DeepEqual(old.Argv, byName.Argv) {
		t.Fatalf("plans differ:\ncto_enter %v\nsession_enter id %v\nsession_enter name %v", old.Argv, byID.Argv, byName.Argv)
	}
	if byID.Note != old.Note {
		t.Fatalf("notes differ: %q and %q", byID.Note, old.Note)
	}
}

// An owner message to the CTO arrives as the owner's words, not as a peer message.
func TestOwnerSendReachesCTOAsTheOwnersWords(t *testing.T) {
	f := startCTOCompactionFixture(t)
	body := "Please review the project status."
	if _, err := f.cli.call("owner_send", protocol.OwnerSendRequest{To: f.n.d.ctoSessionID(), Body: body}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.rec.Env["XDG_DATA_HOME"], "fake-session-"+f.id+".json")
	eventually(t, "owner message in the CTO conversation", func() bool {
		raw, err := os.ReadFile(path)
		return err == nil && strings.Contains(string(raw), body)
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Message from another agent") {
		t.Fatalf("the owner's message was framed as a peer message:\n%s", raw)
	}
	if !strings.Contains(string(raw), body) {
		t.Fatalf("the owner's text did not reach the CTO:\n%s", raw)
	}
}

// session_text masks secrets in the CTO timeline like any other session.
func TestCTOSessionTextMasksSecrets(t *testing.T) {
	f := startCTOCompactionFixture(t)
	f.writeMessages(t, []map[string]any{
		{"type": "user", "text": "use sk-abcdefghijklmnopqrstuvwxyz123456 now", "time": map[string]any{"created": 1000}},
	})
	var page protocol.TextPage
	if err := json.Unmarshal(f.cli.mustCall("session_text", protocol.TextQuery{SessionID: f.n.d.ctoSessionID(), Limit: 50}), &page); err != nil {
		t.Fatal(err)
	}
	for _, e := range page.Entries {
		if strings.Contains(e.Text, "sk-abcdefghijklmnopqrstuvwxyz123456") {
			t.Fatalf("secret not masked: %q", e.Text)
		}
	}
}

// A machine that hosted the CTO before the owner moved it must relay to the
// machine that holds it, not start a second CTO.
func TestCTORoutesToTheMachineThatHostsIt(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{detect: fakeInventory, interval: time.Hour})
	other, _, err := g.gw.Store().CreateNode("other-host", "linux")
	if err != nil {
		t.Fatal(err)
	}
	id, err := g.gw.Store().RegisterCTOSession(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.d.st.db.Exec(`INSERT INTO cto_network(id,session_id) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET session_id=excluded.session_id`, id); err != nil {
		t.Fatal(err)
	}
	if n.d.ctoOnThisNode(context.Background()) {
		t.Fatal("this machine thinks it still holds the CTO")
	}
}
