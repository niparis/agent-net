package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCharterSuggestionsOwnerExample(t *testing.T) {
	g := protocol.Graph{Nodes: []protocol.Node{{Name: "sovl-clone", Charter: protocol.Charter{Needs: "API design"}}, {Name: "homelab", Charter: protocol.Charter{Accepts: "deploys"}}}}
	signals := []protocol.CharterSignal{{From: "sovl-clone", To: "homelab", MessageID: "m_test", Prompt: "get the homelab agent to do its job. all this github token things are his", Outcome: "reject", Reason: "Stop: this work is already in main", ReplyID: "m_reply"}}
	got := charterSuggestions(g, signals)
	b, _ := json.Marshal(got)
	text := string(b)
	for _, want := range []string{"homelab for GitHub secrets", "deploys; GitHub secrets", "already in main", "m_test", "m_reply"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q: %s", want, text)
		}
	}
	if len(got.Suggestions) != 2 {
		t.Fatalf("suggestions: %+v", got)
	}
}

func TestCharterSuggestionsRedirectAndRejection(t *testing.T) {
	g := protocol.Graph{Nodes: []protocol.Node{{Name: "app"}, {Name: "other"}, {Name: "ops"}}}
	signals := []protocol.CharterSignal{{From: "app", To: "other", MessageID: "m1", Outcome: "redirect", RedirectTo: "ops", Reason: "deployment belongs in ops"}, {From: "app", To: "other", MessageID: "m2", Outcome: "reject", Reason: "already in main"}}
	got := charterSuggestions(g, signals)
	b, _ := json.Marshal(got)
	if len(got.Suggestions) != 2 || !strings.Contains(string(b), "deployment") || !strings.Contains(string(b), "already in main") {
		t.Fatalf("%s", b)
	}
}

func TestCharterSuggestionsSizeAndEvidence(t *testing.T) {
	g := protocol.Graph{Nodes: []protocol.Node{{Name: "sender", Charter: protocol.Charter{Needs: strings.Repeat("x", 1900)}}, {Name: "target", Charter: protocol.Charter{Accepts: strings.Repeat("y", 1900)}}}}
	var signals []protocol.CharterSignal
	for i := 0; i < 60; i++ {
		signals = append(signals, protocol.CharterSignal{From: "sender", To: "target", MessageID: fmt.Sprintf("m%d", i), Prompt: fmt.Sprintf("task %d is his job", i), Reason: strings.Repeat("reason ", 50)})
	}
	got := charterSuggestions(g, signals)
	b, err := json.Marshal(got)
	if err != nil || len(b) >= 20000 {
		t.Fatalf("size %d, error %v", len(b), err)
	}
	evidence := map[string]bool{}
	for _, v := range got.Evidence {
		evidence[v.MessageID] = true
	}
	for _, v := range got.Suggestions {
		for _, id := range v.MessageIDs {
			if !evidence[id] {
				t.Fatalf("suggestion without evidence %s", id)
			}
		}
	}
}

func TestCharterSuggestionsTool(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	w := startWorkerNode(t, g, WorkerSettings{}, nil)
	st := g.gw.Store()
	for _, name := range []string{"sovl-clone", "homelab"} {
		if err := st.AddArchitect(name, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	node, _, err := st.CreateNode("fake", "linux")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "claude", NativeID: "s", Repo: protocol.RepoInfo{Project: "sovl-clone"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "h", Repo: protocol.RepoInfo{Project: "homelab"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RememberOwnerPrompt(a, "homelab: github token things are his"); err != nil {
		t.Fatal(err)
	}
	m, _, err := st.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Body: "please"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := w.d.runCTOToolRaw(context.Background(), "charter_suggestions", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	for _, want := range []string{m.ID, "GitHub secrets", "homelab for GitHub secrets"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s in %s", want, raw)
		}
	}
}

func TestCharterSuggestionsExtractJobAndDeduplicate(t *testing.T) {
	g := protocol.Graph{Nodes: []protocol.Node{{Name: "sender"}, {Name: "ops"}}}
	signal := protocol.CharterSignal{From: "sender", To: "ops", MessageID: "m1", Prompt: "ask ops: backups are his job"}
	got := charterSuggestions(g, []protocol.CharterSignal{signal, signal})
	b, _ := json.Marshal(got)
	if len(got.Suggestions) != 2 || strings.Contains(string(b), `"proposed_value":"ask ops: backups"`) {
		t.Fatalf("%s", b)
	}
	for _, v := range got.Suggestions {
		if len(v.MessageIDs) != 1 {
			t.Fatalf("duplicate evidence: %+v", v)
		}
	}
}
