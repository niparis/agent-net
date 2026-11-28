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
func (r ctoPermissionRig) ctoApply(args map[string]any) (protocol.PermissionGrant, error) {
	raw, _ := json.Marshal(map[string]any{"area": "operator", "method": "permission_apply", "arguments": args})
	out, err := r.n.d.runCTOTool(context.Background(), "settings_change", raw)
	var p protocol.PermissionGrant
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
// once with his words: no Desk ask, the rule is in the file, the result names
// what was added and where, and the log line quotes his words. Nothing is
// stored to approve or undo later.
func TestCTOPermissionApplyWritesRule(t *testing.T) {
	r := newCTOPermissionRig(t)
	words := "help wbgpoclead to have merge authority on all his repos"
	ctoOwnerSays(t, r.n.d, "ok, "+words+" please")
	got, err := r.ctoApply(map[string]any{"rules": []string{"Bash(gh pr merge:*)", "Bash(ls)"}, "targets": []string{r.name}, "owner_words": words})
	if err != nil {
		t.Fatalf("permission_apply: %v", err)
	}
	if !strings.HasPrefix(got.ID, "pg_") || got.Status != "applied" || got.OwnerWords != words || got.By != "cto" {
		t.Fatalf("grant: %+v", got)
	}
	if len(got.Results) != 1 || got.Results[0].Status != "applied" || len(got.Results[0].Added) != 1 ||
		got.Results[0].Added[0] != "Bash(gh pr merge:*)" || got.Results[0].File != r.settings {
		t.Fatalf("per target result: %+v", got.Results)
	}
	doc := readPermissionSettings(t, r.repo)
	if countString(doc.Permissions.Allow, "Bash(gh pr merge:*)") != 1 || countString(doc.Permissions.Allow, "Bash(ls)") != 1 ||
		len(doc.Permissions.Deny) != 1 || doc.Other != "keep" {
		t.Fatalf("settings after apply: %+v", doc)
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
}

// The CTO brief tells it to apply a permission the owner gives in its chat,
// with his words, instead of proposing it for later.
func TestCTOBriefAppliesOwnerPermissionWords(t *testing.T) {
	for _, want := range []string{"cto_op method permission_apply", "owner_words", "Never propose for later something he asked"} {
		if !strings.Contains(ctoBrief, want) {
			t.Errorf("ctoBrief lacks %q", want)
		}
	}
}
