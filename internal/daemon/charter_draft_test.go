package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A repo with no charter gets one draft request per version of AGENTS.md. The
// session drafts with agent_charter_draft; the draft stays a suggestion until
// the owner accepts it in the learned list (decision 0087).
func TestCharterDraftRequestDraftAndAccept(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	repo := newRepo(t, "demo")
	agents := filepath.Join(repo, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("# Project: demo\n\nDemo is a small test repo.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, id := helloDelivery(t, n, "caller", repo, "claude-opus-4")
	eventually(t, "session and repo registered", func() bool {
		a, ok, err := g.gw.Store().Agent(id)
		return err == nil && ok && a.Repo.Project == "demo"
	})
	// A thin charter: only the maturity is set. This also makes the repo a graph node.
	if err := g.gw.Store().SetCharter("demo", "maturity", "early", "owner"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	n.d.policy.now = func() time.Time { return now }
	tick := func() { now = now.Add(time.Minute) }

	first := askPolicy(t, client, protocol.PolicyRequest{})
	if strings.Contains(first.Charter, "- maturity:") {
		t.Fatalf("asked for a field that has a value:\n%s", first.Charter)
	}
	for _, want := range []string{"Charter draft request", "AGENTS.md", "agent_charter_draft", "- purpose:", "- accepts:"} {
		if !strings.Contains(first.Charter, want) {
			t.Fatalf("first turn lacks %q:\n%s", want, first.Charter)
		}
	}
	tick()
	if again := askPolicy(t, client, protocol.PolicyRequest{}); strings.Contains(again.Charter, "Charter draft request") {
		t.Fatalf("asked twice for the same AGENTS.md:\n%s", again.Charter)
	}

	// The command line cannot draft.
	cli := cliClient(t, n)
	if _, err := cli.call(protocol.OpCharterDraft, protocol.CharterDraftRequest{Level: "repo", Attr: "purpose", Value: "x"}); err == nil {
		t.Fatalf("cli drafted: %v", err)
	}
	var res protocol.CharterDraftResult
	raw := client.mustCall(protocol.OpCharterDraft, protocol.CharterDraftRequest{Level: "repo", Attr: "purpose", Value: "A small test repo"})
	if err := json.Unmarshal(raw, &res); err != nil || res.ID == 0 || res.Status != "pending" {
		t.Fatalf("draft result: %s %v", raw, err)
	}
	client.mustCall(protocol.OpCharterDraft, protocol.CharterDraftRequest{Level: "agent", Attr: "accepts", Value: "Fixes in demo"})
	// Nothing is set until the owner decides.
	if c, _ := g.gw.Store().EffectiveCharter("demo", "demo", ""); c.Purpose != "" || c.Accepts != "" {
		t.Fatalf("draft took effect: %+v", c)
	}
	drafts, err := g.gw.Store().PendingCharterDrafts()
	if err != nil || len(drafts) != 2 {
		t.Fatalf("pending: %+v %v", drafts, err)
	}

	// The owner list shows the draft and accept writes it.
	var list []protocol.LearnedSuggestion
	if err := json.Unmarshal(cli.mustCall("learned_list", map[string]any{}), &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range list {
		if v.ID == -res.ID && v.Origin == "draft" && v.Level == "repo" && v.NewValue == "A small test repo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("learned list lacks the draft: %+v", list)
	}
	cli.mustCall("learned_answer", map[string]any{"suggestion_id": -res.ID, "action": "accept"})
	if c, _ := g.gw.Store().EffectiveCharter("demo", "demo", ""); c.Purpose != "A small test repo" {
		t.Fatalf("accepted draft: %+v", c)
	}

	// A changed AGENTS.md asks again, for the fields that are still empty.
	if err := os.WriteFile(agents, []byte("# Project: demo\n\nDemo is a small test repo.\n\nMore text.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick()
	next := askPolicy(t, client, protocol.PolicyRequest{})
	if !strings.Contains(next.Charter, "Charter draft request") || strings.Contains(next.Charter, "- purpose:") {
		t.Fatalf("changed AGENTS.md:\n%s", next.Charter)
	}
}
