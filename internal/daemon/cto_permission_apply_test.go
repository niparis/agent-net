package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// ctoPermissionRig is one machine with a registered CTO session and one
// registered repository whose settings.local.json already holds rules.
type ctoPermissionRig struct {
	g        *testGateway
	n        *node
	cli      *localClient
	repo     string
	name     string
	settings string
	before   string
}

func newCTOPermissionRig(t *testing.T) ctoPermissionRig {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	if err := n.d.registerCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	cli := cliClient(t, n)
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{repo}})
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(repo, ".claude", "settings.local.json")
	before := `{"permissions":{"allow":["Bash(ls)"],"deny":["Bash(rm -rf *)"]},"other":"keep"}`
	if err := os.WriteFile(settings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	return ctoPermissionRig{g: g, n: n, cli: cli, repo: repo, name: filepath.Base(repo), settings: settings, before: before}
}

// ctoApply runs cto_op permission_apply the way the CTO's tool does: the owner
// operation path with the CTO as the actor.
func (r ctoPermissionRig) ctoApply(args map[string]any) (protocol.PermissionProposal, error) {
	raw, _ := json.Marshal(map[string]any{"area": "operator", "method": "permission_apply", "arguments": args})
	out, err := r.n.d.runCTOTool(context.Background(), "settings_change", raw)
	var p protocol.PermissionProposal
	if err != nil {
		return p, err
	}
	data, merr := json.Marshal(out)
	if merr != nil {
		return p, merr
	}
	return p, json.Unmarshal(data, &p)
}

// The owner tells the CTO in its chat to allow a rule. The CTO applies it at
// once with his words as the record: no Desk ask, the rule is in the file, the
// result names what was added and where, and the existing undo removes only
// the added rule.
func TestCTOPermissionApplyWritesRuleAndUndoes(t *testing.T) {
	r := newCTOPermissionRig(t)
	words := "help wbgpoclead to have merge authority on all his repos"
	ctoOwnerSays(t, r.n.d, "ok, "+words+" please")
	got, err := r.ctoApply(map[string]any{"rules": []string{"Bash(gh pr merge:*)", "Bash(ls)"}, "targets": []string{r.name}, "owner_words": words})
	if err != nil {
		t.Fatalf("permission_apply: %v", err)
	}
	if !strings.HasPrefix(got.ID, "pp_") || got.Status != "applied" || got.OwnerWords != words || got.DecidedBy != "cto" {
		t.Fatalf("stored result: %+v", got)
	}
	if len(got.Results) != 1 || got.Results[0].Status != "applied" || len(got.Results[0].Added) != 1 ||
		got.Results[0].Added[0] != "Bash(gh pr merge:*)" || got.Results[0].File != r.settings {
		t.Fatalf("per target result: %+v", got.Results)
	}
	doc := readPermissionSettings(t, r.repo)
	if countString(doc.Permissions.Allow, "Bash(gh pr merge:*)") != 1 || countString(doc.Permissions.Allow, "Bash(ls)") != 1 || doc.Other != "keep" {
		t.Fatalf("settings after apply: %+v", doc)
	}
	stored, err := r.g.gw.Store().PermissionProposal(got.ID)
	if err != nil || stored.OwnerWords != words || stored.DecidedBy != "cto" || stored.Status != "applied" {
		t.Fatalf("gateway record: %+v %v", stored, err)
	}
	asks, err := r.g.gw.Store().ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asks {
		if a.Kind == protocol.AskKindPermission {
			t.Fatalf("a direct CTO apply opened a Desk ask: %+v", a)
		}
	}
	log, err := r.g.gw.Store().DeskLog(50)
	if err != nil {
		t.Fatal(err)
	}
	// One log line for the grant: the CTO's, with the owner's words and the undo.
	var undo []string
	for _, e := range log {
		if strings.Contains(e.Text, "permission_apply") {
			t.Fatalf("a second log line repeats the grant: %+v", e)
		}
		if e.Source == "cto" && len(e.Options) == 2 && e.Options[0] == protocol.OpPermissionUndo && strings.Contains(e.Detail, words) {
			undo = e.Options
		}
	}
	if undo == nil {
		t.Fatalf("no CTO Desk log line with the owner's words and an undo: %+v", log)
	}

	r.cli.mustCall(undo[0], json.RawMessage(undo[1]))
	doc = readPermissionSettings(t, r.repo)
	if countString(doc.Permissions.Allow, "Bash(gh pr merge:*)") != 0 || countString(doc.Permissions.Allow, "Bash(ls)") != 1 ||
		len(doc.Permissions.Deny) != 1 || doc.Other != "keep" {
		t.Fatalf("settings after undo: %+v", doc)
	}
	if stored, err := r.g.gw.Store().PermissionProposal(got.ID); err != nil || stored.Status != "undone" {
		t.Fatalf("record after undo: %+v %v", stored, err)
	}
}

// The owner's words are the record of his approval, so an apply without them
// is refused and writes nothing.
func TestCTOPermissionApplyNeedsOwnerWords(t *testing.T) {
	r := newCTOPermissionRig(t)
	for _, words := range []string{"", "   "} {
		if _, err := r.ctoApply(map[string]any{"rules": []string{"Bash(gh pr merge:*)"}, "targets": []string{r.name}, "owner_words": words}); err == nil || !strings.Contains(err.Error(), "owner_words") {
			t.Fatalf("owner_words %q: want a refusal naming owner_words, got %v", words, err)
		}
	}
	if raw, err := os.ReadFile(r.settings); err != nil || string(raw) != r.before {
		t.Fatalf("settings changed: %v\n%s", err, raw)
	}
	// A peer session has no route to the op at all.
	pi, _ := helloPi(t, r.n, "peer", r.repo, false)
	if _, err := pi.call("permission_apply", map[string]any{"rules": []string{"Bash(gh pr merge:*)"}, "targets": []string{r.name}, "owner_words": "yes"}); err == nil {
		t.Fatal("a peer session applied a permission")
	}
	if raw, err := os.ReadFile(r.settings); err != nil || string(raw) != r.before {
		t.Fatalf("settings changed by a peer: %v\n%s", err, raw)
	}
}

// Words the owner never typed in the CTO chat do not count as his approval:
// the apply is refused with one line, writes nothing and opens no Desk ask.
func TestCTOPermissionApplyRefusesWordsNeverTyped(t *testing.T) {
	r := newCTOPermissionRig(t)
	ctoOwnerSays(t, r.n.d, "what rules does the garden repo have?")
	_, err := r.ctoApply(map[string]any{"rules": []string{"Bash(gh pr merge:*)"}, "targets": []string{r.name}, "owner_words": "let garden merge its PRs"})
	if err == nil || !strings.Contains(err.Error(), "was not found in what the owner typed") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("want a one-line refusal, got %v", err)
	}
	if raw, err := os.ReadFile(r.settings); err != nil || string(raw) != r.before {
		t.Fatalf("settings changed: %v\n%s", err, raw)
	}
	asks, err := r.g.gw.Store().ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asks {
		if a.Kind == protocol.AskKindPermission {
			t.Fatalf("a refused apply opened a Desk ask: %+v", a)
		}
	}
}

// A proposal already on the Desk, filed for the owner's own chat instruction,
// is applied by id on his words. Its Desk item closes with an answer that
// names the CTO and his words, and the CTO sees it in cto_proposals_find and
// proposal_ask by its pp_ id.
func TestCTOPermissionApplyApprovesDeskProposal(t *testing.T) {
	r := newCTOPermissionRig(t)
	pi, _ := helloPi(t, r.n, "np", r.repo, false)
	var proposed protocol.PermissionProposeResult
	if err := json.Unmarshal(pi.mustCall(protocol.OpPermissionPropose, protocol.PermissionProposeRequest{
		Rules: []string{"Bash(gh pr merge:*)"}, Targets: []string{r.name}, Reason: "merge authority",
	}), &proposed); err != nil || proposed.ID == "" {
		t.Fatalf("propose: %+v %v", proposed, err)
	}

	ctx := context.Background()
	page, err := r.n.d.readCTOTool(ctx, "cto_proposals_find", json.RawMessage(`{"kind":"permission"}`))
	if err != nil {
		t.Fatalf("cto_proposals_find: %v", err)
	}
	listed, _ := json.Marshal(page)
	if !strings.Contains(string(listed), `"id":"`+proposed.ID+`"`) || !strings.Contains(string(listed), `"status":"pending"`) ||
		!strings.Contains(string(listed), "permission_apply") {
		t.Fatalf("cto_proposals_find does not list %s with its state and apply call: %s", proposed.ID, listed)
	}
	one, err := r.n.d.readCTOTool(ctx, "cto_proposals_find", json.RawMessage(`{"id":"`+proposed.ID+`"}`))
	if err != nil {
		t.Fatalf("cto_proposals_find by pp_ id: %v", err)
	}
	if raw, _ := json.Marshal(one); !strings.Contains(string(raw), `"status":"pending"`) {
		t.Fatalf("cto_proposals_find by id: %s", raw)
	}
	asked, err := r.n.d.runCTOTool(ctx, "proposal_ask", json.RawMessage(`{"id":"`+proposed.ID+`"}`))
	if err != nil {
		t.Fatalf("proposal_ask on a pp_ id: %v", err)
	}
	if raw, _ := json.Marshal(asked); !strings.Contains(string(raw), "permission_apply") || !strings.Contains(string(raw), "pending") {
		t.Fatalf("proposal_ask on a pp_ id does not point at permission_apply: %s", raw)
	}

	words := "help wbgpoclead to have merge authority on all his repos"
	ctoOwnerSays(t, r.n.d, words)
	if _, err := r.ctoApply(map[string]any{"id": proposed.ID, "rules": []string{"Bash(x)"}, "owner_words": words}); err == nil {
		t.Fatal("an id with new rules was accepted")
	}
	got, err := r.ctoApply(map[string]any{"id": proposed.ID, "owner_words": words})
	if err != nil {
		t.Fatalf("approve by id: %v", err)
	}
	if got.ID != proposed.ID || got.Status != "applied" || got.OwnerWords != words || got.DecidedBy != "cto" {
		t.Fatalf("approved proposal: %+v", got)
	}
	if countString(readPermissionSettings(t, r.repo).Permissions.Allow, "Bash(gh pr merge:*)") != 1 {
		t.Fatal("rule not written")
	}
	asks, err := r.g.gw.Store().ListAsks()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range asks {
		if a.Kind == protocol.AskKindPermission {
			t.Fatalf("Desk item still open: %+v", a)
		}
	}
	items, err := r.g.gw.Store().ListInbox()
	if err != nil {
		t.Fatal(err)
	}
	var closed protocol.InboxItem
	for _, item := range items {
		if item.Kind == protocol.AskKindPermission {
			closed = item
		}
	}
	if closed.Status != "approved" || !strings.Contains(closed.Answer, "CTO") || !strings.Contains(closed.Answer, words) {
		t.Fatalf("Desk item closed as %q with answer %q", closed.Status, closed.Answer)
	}
	if _, err := r.ctoApply(map[string]any{"id": proposed.ID, "owner_words": words}); err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("second approve: %v", err)
	}
}

// The CTO brief tells it to apply a permission the owner gives in its chat,
// with his words, instead of filing a Desk proposal.
func TestCTOBriefAppliesOwnerPermissionWords(t *testing.T) {
	for _, want := range []string{"cto_op method permission_apply", "owner_words", "do not file a Desk proposal", "permission_undo"} {
		if !strings.Contains(ctoBrief, want) {
			t.Errorf("ctoBrief lacks %q", want)
		}
	}
}
