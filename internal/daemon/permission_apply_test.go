package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/claudetrust"
	"github.com/niparis/agent-net/internal/protocol"
)

type permissionSettings struct {
	Permissions struct {
		Allow []string `json:"allow"`
		Deny  []string `json:"deny"`
	} `json:"permissions"`
	Other string `json:"other"`
}

func readPermissionSettings(t *testing.T, repo string) permissionSettings {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var doc permissionSettings
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("settings are not JSON: %v\n%s", err, raw)
	}
	return doc
}

func countString(list []string, want string) int {
	n := 0
	for _, v := range list {
		if v == want {
			n++
		}
	}
	return n
}

// The whole flow: a session proposes, the owner clicks approve, the target
// machine merges the rules, the Desk log records it, and undo removes only the
// rule the approval added.
func TestPermissionProposalOwnerClickAppliesAndUndoes(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	cli := cliClient(t, n)

	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	name := filepath.Base(repo)
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{repo}})

	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	settingsFile := filepath.Join(repo, ".claude", "settings.local.json")
	before := `{"permissions":{"allow":["Bash(ls)","Bash(gh pr merge *)"],"deny":["Bash(rm -rf *)"]},"other":"keep"}`
	if err := os.WriteFile(settingsFile, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}

	pi, piSession := helloPi(t, n, "np", repo, false)
	rules := []string{"Bash(gh pr merge *)", "Bash(gh pr close *)"}
	raw := pi.mustCall(protocol.OpPermissionPropose, protocol.PermissionProposeRequest{
		Rules:   rules,
		Targets: []string{name},
		Reason:  "the forge repos need PR merges",
	})
	var proposal protocol.PermissionProposeResult
	if err := json.Unmarshal(raw, &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ID == "" || proposal.Status != "pending" || len(proposal.Targets) != 1 || proposal.Targets[0].Path != repo {
		t.Fatalf("proposal: %+v", proposal)
	}

	var inbox struct {
		Items []protocol.InboxItem `json:"items"`
	}
	if err := json.Unmarshal(cli.mustCall("inbox_list", nil), &inbox); err != nil {
		t.Fatal(err)
	}
	var ask protocol.InboxItem
	for _, item := range inbox.Items {
		if item.Kind == protocol.AskKindPermission {
			ask = item
		}
	}
	if ask.ID == "" || ask.Kind != protocol.AskKindPermission || ask.Status != "open" {
		t.Fatalf("permission ask missing: %+v", inbox.Items)
	}
	for _, want := range []string{"Bash(gh pr merge *)", "Bash(gh pr close *)", name, n.cfg.Hostname, repo,
		"the forge repos need PR merges", piSession, "restart"} {
		if !strings.Contains(ask.Text, want) {
			t.Errorf("ask text does not name %q:\n%s", want, ask.Text)
		}
	}
	if len(ask.Options) != 2 || ask.Options[0] != "approve" || ask.Options[1] != "reject" {
		t.Fatalf("ask options: %v", ask.Options)
	}
	var asks protocol.AsksResult
	if err := json.Unmarshal(cli.mustCall(protocol.OpAsksList, nil), &asks); err != nil {
		t.Fatal(err)
	}
	ownerAsk := false
	for _, a := range asks.Asks {
		if a.ID == ask.ID {
			ownerAsk = a.Next == protocol.AskNextOwner && a.Kind == protocol.AskKindPermission
		}
	}
	if !ownerAsk {
		t.Fatalf("permission ask is not waiting for the owner: %+v", asks.Asks)
	}

	// No click yet: a non-owner answer, a direct apply frame and an undo from
	// the session all write nothing.
	if _, err := pi.call("inbox_answer", map[string]string{"id": ask.ID, "answer": "approve"}); err == nil {
		t.Fatal("an agent session answered the owner's permission ask")
	}
	if _, err := pi.call(protocol.FramePermissionApply, protocol.PermissionApplyFrame{
		ProposalID: proposal.ID, Target: proposal.Targets[0], Rules: rules,
	}); err == nil {
		t.Fatal("a session sent a direct permission apply frame")
	}
	if _, err := pi.call(protocol.OpPermissionUndo, protocol.PermissionUndoRequest{ID: proposal.ID}); err == nil {
		t.Fatal("an agent session undid a permission proposal")
	}
	if raw, err := os.ReadFile(settingsFile); err != nil || string(raw) != before {
		t.Fatalf("settings changed before the owner clicked: %v\n%s", err, raw)
	}

	// The owner clicks approve. The gateway tells this machine to write.
	var answered struct {
		Item              protocol.InboxItem               `json:"item"`
		PermissionResults []protocol.PermissionApplyResult `json:"permission_results"`
		PermissionError   string                           `json:"permission_error"`
	}
	if err := json.Unmarshal(cli.mustCall("inbox_answer", map[string]string{"id": ask.ID, "answer": "approve"}), &answered); err != nil {
		t.Fatal(err)
	}
	if answered.PermissionError != "" {
		t.Fatalf("apply error: %s", answered.PermissionError)
	}
	if len(answered.PermissionResults) != 1 || answered.PermissionResults[0].Status != "applied" {
		t.Fatalf("apply results: %+v", answered.PermissionResults)
	}
	added := answered.PermissionResults[0].Added
	if len(added) != 1 || added[0] != "Bash(gh pr close *)" {
		t.Fatalf("the approval added %v, want only Bash(gh pr close *)", added)
	}
	doc := readPermissionSettings(t, repo)
	if countString(doc.Permissions.Allow, "Bash(gh pr close *)") != 1 || countString(doc.Permissions.Allow, "Bash(gh pr merge *)") != 1 ||
		countString(doc.Permissions.Allow, "Bash(ls)") != 1 {
		t.Fatalf("merged allow list: %v", doc.Permissions.Allow)
	}
	if len(doc.Permissions.Deny) != 1 || doc.Permissions.Deny[0] != "Bash(rm -rf *)" || doc.Other != "keep" {
		t.Fatalf("other settings changed: %+v", doc)
	}
	exclude, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(exclude), "/.claude/settings.local.json") {
		t.Fatalf("settings file is not ignored: %v\n%s", err, exclude)
	}

	var log protocol.DeskLogResult
	if err := json.Unmarshal(cli.mustCall(protocol.OpDeskLog, protocol.DeskLogQuery{Limit: 20}), &log); err != nil {
		t.Fatal(err)
	}
	var entry protocol.DeskLogEntry
	for _, e := range log.Entries {
		if len(e.Options) == 2 && e.Options[0] == protocol.OpPermissionUndo {
			entry = e
		}
	}
	if entry.ID == 0 || !strings.Contains(entry.Text, "Allowed Claude Code permissions") || !strings.Contains(entry.Detail, "applied") {
		t.Fatalf("permission log entry: %+v", log.Entries)
	}
	var undo protocol.PermissionUndoRequest
	if err := json.Unmarshal([]byte(entry.Options[1]), &undo); err != nil || undo.ID != proposal.ID {
		t.Fatalf("undo option: %s %v", entry.Options[1], err)
	}

	// Undo removes only the rule this approval added.
	if _, err := cli.call(protocol.OpPermissionUndo, undo); err != nil {
		t.Fatalf("undo: %v", err)
	}
	doc = readPermissionSettings(t, repo)
	if countString(doc.Permissions.Allow, "Bash(gh pr close *)") != 0 ||
		countString(doc.Permissions.Allow, "Bash(gh pr merge *)") != 1 || countString(doc.Permissions.Allow, "Bash(ls)") != 1 {
		t.Fatalf("allow after undo: %v", doc.Permissions.Allow)
	}
	if len(doc.Permissions.Deny) != 1 || doc.Other != "keep" {
		t.Fatalf("other settings changed by undo: %+v", doc)
	}
	if direct, derr := g.gw.Store().DeskLog(20); derr != nil || len(direct) == 0 || len(direct[0].Options) != 0 {
		t.Fatalf("gateway log after undo (direct): %+v %v", direct, derr)
	}
	var after protocol.DeskLogResult
	if err := json.Unmarshal(cli.mustCall(protocol.OpDeskLog, protocol.DeskLogQuery{Limit: 21}), &after); err != nil {
		t.Fatal(err)
	}
	settled := false
	for _, e := range after.Entries {
		if e.ID == entry.ID {
			settled = strings.HasPrefix(e.Text, "Undone: ") && len(e.Options) == 0
		}
	}
	if !settled {
		t.Fatalf("log line after undo: %+v", after.Entries)
	}
}

// Folder trust goes through the same owner click and the owner's existing trust
// writer. A folder the owner already trusted is left alone by the undo.
func TestTrustProposalOwnerClickAppliesThroughExistingWriterAndUndoes(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := folderTrustEnv(t)
	n := startWorkerNode(t, g, shortSet, nil)
	fresh := newHomeRepo(t, home, "code/fresh")
	already := newHomeRepo(t, home, "code/already")
	if err := claudetrust.Set(already); err != nil {
		t.Fatal(err)
	}

	pi, _ := helloPi(t, n.node, "nt", home, false)
	raw := pi.mustCall(protocol.OpPermissionPropose, protocol.PermissionProposeRequest{
		Kind:    protocol.PermissionKindTrust,
		Machine: n.cfg.Hostname,
		Targets: []string{fresh, already},
		Reason:  "Claude Code cannot start in these folders",
	})
	var proposal protocol.PermissionProposeResult
	if err := json.Unmarshal(raw, &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ID == "" || len(proposal.Targets) != 2 {
		t.Fatalf("trust proposal: %+v", proposal)
	}
	var inbox struct {
		Items []protocol.InboxItem `json:"items"`
	}
	if err := json.Unmarshal(n.cli.mustCall("inbox_list", nil), &inbox); err != nil {
		t.Fatal(err)
	}
	var ask protocol.InboxItem
	for _, item := range inbox.Items {
		if item.Kind == protocol.AskKindPermission && strings.Contains(item.Text, fresh) {
			ask = item
		}
	}
	if ask.ID == "" || !strings.Contains(ask.Text, already) || !strings.Contains(ask.Text, n.cfg.Hostname) ||
		!strings.Contains(ask.Text, "Claude Code cannot start in these folders") {
		t.Fatalf("trust ask: %+v", ask)
	}

	// No click yet: a non-owner answer and a direct apply frame write nothing.
	if _, err := pi.call("inbox_answer", map[string]string{"id": ask.ID, "answer": "approve"}); err == nil {
		t.Fatal("an agent session answered the owner's trust ask")
	}
	if _, err := pi.call(protocol.FramePermissionApply, protocol.PermissionApplyFrame{
		ProposalID: proposal.ID, Kind: protocol.PermissionKindTrust,
		Target: protocol.PermissionTarget{Path: fresh},
	}); err == nil {
		t.Fatal("a session sent a direct trust apply frame")
	}
	if trusted, _ := claudetrust.Trusted(fresh); trusted {
		t.Fatal("the folder was trusted before the owner clicked")
	}
	if trusted, _ := claudetrust.Trusted(already); !trusted {
		t.Fatal("the owner's earlier trust was lost")
	}

	var answered struct {
		PermissionResults []protocol.PermissionApplyResult `json:"permission_results"`
		PermissionError   string                           `json:"permission_error"`
	}
	if err := json.Unmarshal(n.cli.mustCall("inbox_answer", map[string]string{"id": ask.ID, "answer": "approve"}), &answered); err != nil {
		t.Fatal(err)
	}
	if answered.PermissionError != "" || len(answered.PermissionResults) != 2 {
		t.Fatalf("trust apply: %+v %s", answered.PermissionResults, answered.PermissionError)
	}
	if trusted, err := claudetrust.Trusted(fresh); err != nil || !trusted {
		t.Fatalf("fresh folder not trusted: %v %v", trusted, err)
	}
	if trusted, err := claudetrust.Trusted(already); err != nil || !trusted {
		t.Fatalf("already trusted folder lost: %v %v", trusted, err)
	}

	var log protocol.DeskLogResult
	if err := json.Unmarshal(n.cli.mustCall(protocol.OpDeskLog, protocol.DeskLogQuery{Limit: 20}), &log); err != nil {
		t.Fatal(err)
	}
	var undo protocol.PermissionUndoRequest
	for _, e := range log.Entries {
		if len(e.Options) == 2 && e.Options[0] == protocol.OpPermissionUndo {
			_ = json.Unmarshal([]byte(e.Options[1]), &undo)
		}
	}
	if undo.ID != proposal.ID {
		t.Fatalf("trust log entry has no undo: %+v", log.Entries)
	}
	if _, err := n.cli.call(protocol.OpPermissionUndo, undo); err != nil {
		t.Fatalf("trust undo: %v", err)
	}
	if trusted, _ := claudetrust.Trusted(fresh); trusted {
		t.Fatal("undo did not remove the trust the approval added")
	}
	if trusted, _ := claudetrust.Trusted(already); !trusted {
		t.Fatal("undo removed a trust that was there before")
	}
}

// A needs_trust start raises exactly one trust ask for its machine and folder.
func TestNeedsTrustRaisesOneDedupedAsk(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	home := pathTestHome(t)
	c := startClaudeNode(t, g, shortSet)
	repo := newHomeRepo(t, home, "code/wbg")
	r := sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{Path: repo, Harness: "claude"})
	if r.Status != "needs_trust" || r.Path != repo {
		t.Fatalf("untrusted start: %+v", r)
	}
	asks := func() []protocol.Ask {
		t.Helper()
		var list protocol.AsksResult
		if err := json.Unmarshal(c.cli.mustCall(protocol.OpAsksList, nil), &list); err != nil {
			t.Fatal(err)
		}
		return list.Asks
	}
	count := func(list []protocol.Ask) int {
		n := 0
		for _, a := range list {
			if a.Kind == protocol.AskKindPermission && strings.Contains(a.Question, repo) {
				n++
			}
		}
		return n
	}
	eventually(t, "one trust ask after the first needs_trust", func() bool { return count(asks()) == 1 })
	if got := count(asks()); got != 1 {
		t.Fatalf("trust asks after the first needs_trust: %d", got)
	}
	r = sessionStartPathCall(t, c.workerNode, protocol.SessionStartRequest{Path: repo, Harness: "claude"})
	if r.Status != "needs_trust" {
		t.Fatalf("second untrusted start: %+v", r)
	}
	if got := count(asks()); got != 1 {
		t.Fatalf("trust asks after the second needs_trust: %d", got)
	}
	var ask protocol.Ask
	for _, a := range asks() {
		if a.Kind == protocol.AskKindPermission && strings.Contains(a.Question, repo) {
			ask = a
		}
	}
	if !strings.Contains(ask.Question, c.cfg.Hostname) || !strings.Contains(ask.Question, "needs Claude Code folder trust") {
		t.Fatalf("automatic trust ask text: %s", ask.Question)
	}
	if trusted, _ := claudetrust.Trusted(repo); trusted {
		t.Fatal("needs_trust alone trusted the folder")
	}
	if _, err := c.cli.call("inbox_answer", map[string]string{"id": ask.ID, "answer": "approve"}); err != nil {
		t.Fatalf("approve the automatic trust ask: %v", err)
	}
	if trusted, err := claudetrust.Trusted(repo); err != nil || !trusted {
		t.Fatalf("automatic trust ask did not trust the folder: %v %v", trusted, err)
	}
	if _, err := c.cli.call(protocol.OpPermissionUndo, protocol.PermissionUndoRequest{ID: strings.TrimPrefix(ask.Ref, "permission:")}); err != nil {
		t.Fatalf("undo the automatic trust: %v", err)
	}
	if trusted, _ := claudetrust.Trusted(repo); trusted {
		t.Fatal("undo did not remove the automatic trust")
	}
}

// An unregistered target is refused at proposal time and named, before any ask
// is opened.
func TestPermissionProposalRejectsUnregisteredTarget(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	cli := cliClient(t, n)
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{repo}})
	pi, _ := helloPi(t, n, "np", repo, false)

	_, err := pi.call(protocol.OpPermissionPropose, protocol.PermissionProposeRequest{
		Rules: []string{"Bash(gh pr merge *)"}, Targets: []string{"missing-repo"}, Reason: "merge PRs",
	})
	if err == nil || !strings.Contains(err.Error(), "missing-repo") {
		t.Fatalf("unregistered target: %v", err)
	}
	var inbox struct {
		Items []protocol.InboxItem `json:"items"`
	}
	if err := json.Unmarshal(cli.mustCall("inbox_list", nil), &inbox); err != nil {
		t.Fatal(err)
	}
	for _, item := range inbox.Items {
		if item.Kind == protocol.AskKindPermission {
			t.Fatalf("an ask was opened for a refused target: %+v", item)
		}
	}
}

// A session whose role node holds the permissions scope over its repo has the
// proposal approved by that scope: the rules reach the repo's settings file at
// once, the proposal is decided by the scope, and the owner gets no ask.
func TestPermissionProposalFromAScopeHolderAppliesWithoutAnAsk(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cmdline: "pi"})
	if _, err := n.d.call(context.Background(), "hello", map[string]any{"hostname": "fixture", "version": "v0.4.145", "graph_refs": true}); err != nil {
		t.Fatal(err)
	}
	waitGraphCutover(t, g)
	cli := cliClient(t, n)
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	name := filepath.Base(repo)
	cli.mustCall("project_add", protocol.ProjectAddRequest{Paths: []string{repo}})
	pi, piSession := helloPi(t, n, "np", repo, false)
	st := g.gw.Store()
	view, ok, err := st.Agent(piSession)
	if err != nil || !ok || view.AgentID == "" {
		t.Fatalf("the session has no role node: %+v %v", view, err)
	}
	if _, err := st.SetScope(protocol.ScopeSetRequest{Node: view.AgentID, Kind: protocol.ScopePermissions, Over: "repo:" + name}, "owner", false); err != nil {
		t.Fatal(err)
	}
	propose := func(rule string) protocol.PermissionProposeResult {
		var res protocol.PermissionProposeResult
		raw := pi.mustCall(protocol.OpPermissionPropose, protocol.PermissionProposeRequest{Rules: []string{rule}, Targets: []string{name}, Reason: "merge PRs"})
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := propose("Bash(gh pr merge:*)")
	if res.Status != "applied" {
		t.Fatalf("proposal: %+v", res)
	}
	if got := readPermissionSettings(t, repo); countString(got.Permissions.Allow, "Bash(gh pr merge:*)") != 1 {
		t.Fatalf("settings after the scope approved it: %+v", got)
	}
	for _, it := range func() []protocol.InboxItem { items, _ := st.ListInbox(); return items }() {
		if it.Kind == protocol.AskKindPermission && it.Status == "open" {
			t.Fatalf("the owner got an ask: %+v", it)
		}
	}
	// A rule on the never list still waits for the owner.
	if res := propose("Bash(git push --force:*)"); res.Status != "pending" {
		t.Fatalf("a never-list rule: %+v", res)
	}
}
