package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMergerStartStopProposal(t *testing.T) {
	for _, tc := range []struct{ remote, optional bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
		remote := tc.remote
		t.Run(map[bool]string{false: "local", true: "remote"}[remote]+map[bool]string{false: "-default", true: "-optional"}[tc.optional], func(t *testing.T) {
			g := startGateway(t, t.TempDir(), "")
			caller := startClaudeNode(t, g, shortSet)
			target := caller
			if remote {
				target = startClaudeNode(t, g, shortSet)
			}
			repo := newRepo(t, "garden")
			target.register(t, repo)
			trustClaudeFolder(t, repo)
			t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", target.sessions)
			if tc.optional {
				if _, err := g.gw.Store().WriteSettings([]protocol.SettingsChange{{Key: "delivery.tests_before_merge", Level: "repo", Scope: "garden", Value: json.RawMessage(`"optional"`)}}, "owner", "test disabled tests"); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			args, _ := json.Marshal(map[string]any{"action": "merger_start", "project": "garden", "node_id": target.d.NodeID()})
			if _, err := legacyProposalFixture(ctx, caller.d, "session_change", args); err != nil {
				t.Fatal(err)
			}
			items, err := caller.d.proposalRunner().list()
			if err != nil || len(items) != 1 {
				t.Fatalf("proposals=%v err=%v", items, err)
			}
			if !strings.Contains(items[0].Reason, "garden merger: claude · anthropic · claude-opus-5-5 session in garden on ") || strings.Contains(items[0].Reason, "default pipeline") != tc.optional {
				t.Fatalf("approval=%s", items[0].Reason)
			}
			if _, err := os.Stat(filepath.Join(repo, "fake-claude.json")); !os.IsNotExist(err) {
				t.Fatal("started before approval")
			}
			if err := caller.d.proposalRunner().decide(ctx, items[0].ID, true); err != nil {
				t.Fatal(err)
			}
			items, err = caller.d.proposalRunner().list()
			if err != nil {
				t.Fatal(err)
			}
			var started protocol.StartedSession
			if err := json.Unmarshal(items[0].Result, &started); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				raw, _ := os.ReadFile(filepath.Join(started.Path, "fake-claude.json"))
				var rec fakeClaudeRecord
				if json.Unmarshal(raw, &rec) == nil && rec.Pgid > 1 {
					syscall.Kill(-rec.Pgid, syscall.SIGKILL)
				}
			})
			eventually(t, "merger registration", func() bool {
				var err error
				started, err = caller.d.sessionStartGet(ctx, started.ID)
				if err != nil {
					t.Fatal(err)
				}
				if started.Status == "failed" {
					t.Fatalf("start=%+v", started)
				}
				return started.Status == "started"
			})
			a, ok, err := g.gw.Store().Agent(started.SessionID)
			if err != nil || !ok || a.Type != "merger" || a.Profile.Name != "garden merger" || a.NodeID != target.d.NodeID() {
				t.Fatalf("merger=%+v ok=%v err=%v", a, ok, err)
			}
			graph, err := g.gw.Store().Graph()
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"accepts", "tests", "merges"} {
				want := "/delivery/merger"
				if kind == "accepts" {
					want = "/delivery/caller"
				}
				found := false
				for _, edge := range graph.Edges {
					if edge.To == "garden" && edge.Kind == kind && edge.From == want {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing %s owner %s", kind, want)
				}
			}
			rec := readClaude(t, started.Path)
			if argValue(rec.Args, "--name") != "garden merger" || argValue(rec.Args, "--model") != "claude-opus-5-5" || rec.Args[len(rec.Args)-1] != MergerFirstMessage {
				t.Fatalf("args=%v", rec.Args)
			}
			env, _ := rec.Settings["env"].(map[string]any)
			if got := env["GOCACHE"]; got != target.d.workerGoCacheDir() {
				t.Fatalf("merger GOCACHE = %v, want %s", got, target.d.workerGoCacheDir())
			}
			if started.EnterPlan != nil {
				t.Fatalf("merger opened a view: %+v", started.EnterPlan)
			}
			raw, err := target.d.call(ctx, "merger.plan", map[string]string{"project": "garden"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"needs_default":true`) {
				t.Fatalf("pipeline still off: %s", raw)
			}
			args, _ = json.Marshal(map[string]any{"action": "merger_stop", "project": "garden", "node_id": target.d.NodeID()})
			if _, err := legacyProposalFixture(ctx, caller.d, "session_change", args); err != nil {
				t.Fatal(err)
			}
			items, err = caller.d.proposalRunner().list()
			if err != nil {
				t.Fatal(err)
			}
			if err := caller.d.proposalRunner().decide(ctx, items[len(items)-1].ID, true); err != nil {
				t.Fatal(err)
			}
			a, _, err = g.gw.Store().Agent(started.SessionID)
			if err != nil || a.Type != "agent" {
				t.Fatalf("stop=%+v err=%v", a, err)
			}
			graph, err = g.gw.Store().Graph()
			if err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"tests", "merges"} {
				found := false
				for _, edge := range graph.Edges {
					if edge.To == "garden" && edge.Kind == kind && edge.From == "/delivery/caller" {
						found = true
					}
				}
				if !found {
					t.Fatalf("stop did not return %s to caller", kind)
				}
			}
		})
	}
}

func TestMergerCTORecipe(t *testing.T) {
	for _, text := range []string{"merger_start", "tester-merger", "a session that tests and merges", "ask which project"} {
		if !strings.Contains(ctoBrief, text) {
			t.Errorf("CTO recipe lacks %q", text)
		}
	}
}

func TestMergerFirstMessageContracts(t *testing.T) {
	for _, rule := range []string{"Each step task includes its contract: follow it.", "Never force-push", "Never use the owner's checkout", "Never commit build outputs, binaries or secrets", "Report every step"} {
		if !strings.Contains(MergerFirstMessage, rule) {
			t.Errorf("missing %q", rule)
		}
	}
	for _, rule := range []string{"For release, follow", "renumber mechanically", "resolves the conflict in its own merge worktree", "merge-<change id>"} {
		if strings.Contains(MergerFirstMessage, rule) {
			t.Errorf("private step rule %q", rule)
		}
	}
}

func TestMergerFirstMessageBatchRules(t *testing.T) {
	for _, rule := range []string{"merge-batch-<n>", "git diff --name-only", "--no-ff", "batch tip", "fields.tested_commit", "change head", "one at a time", "sensitive paths", "depend on each other", "CI link"} {
		if !strings.Contains(MergerFirstMessage, rule) {
			t.Errorf("missing batch rule %q", rule)
		}
	}
}

// The merger prompt carries the train rule, including the local bisect before
// any re-push. It stays out of the shared guidance block.
func TestMergerFirstMessageMergeTrain(t *testing.T) {
	for _, rule := range []string{
		"train mode",
		"test each change locally with the targeted packages",
		"Run CI once on the train tip",
		"Cite that one run for every change in the train",
		"bisect locally to find the culprit before you re-push",
	} {
		if !strings.Contains(MergerFirstMessage, rule) {
			t.Errorf("missing train rule %q", rule)
		}
	}
}

// A merger runs in its own git worktree under the daemon's state folder, never
// in the project's folder, where the lead and the owner run. Claude Code keys
// its memory by folder, so a shared folder shares memory (decision 0157).
func TestMergerRunsInItsOwnFolder(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	c := startClaudeNode(t, g, shortSet)
	repo := newRepo(t, "garden")
	c.register(t, repo)
	trustClaudeFolder(t, repo)
	t.Setenv("AGENT_NET_CLAUDE_SESSIONS_DIR", c.sessions)
	ctx := context.WithValue(context.Background(), proposalActorKey{}, "owner (CTO proposal p_1)")
	value, err := c.d.executeApprovedCTOTool(ctx, "merger_start", json.RawMessage(`{"project":"garden","folder":"worktree","branch":"hidden-branch","create":true}`))
	if err != nil {
		t.Fatal(err)
	}
	started := value.(protocol.StartedSession)
	want := filepath.Join(c.cfg.StateDir, "mergers", "garden")
	if started.Path != want || started.Path == repo || started.Branch == "hidden-branch" {
		t.Fatalf("merger folder: %+v, want %s", started, want)
	}
	common, err := gitOut(ctx, want, "rev-parse", "--path-format=absolute", "--git-common-dir")
	gitDir, _ := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
	if resolved, _ := filepath.EvalSymlinks(common); err != nil || resolved != gitDir {
		t.Fatalf("merger folder is not a worktree of %s: %q %v", repo, common, err)
	}
	t.Cleanup(func() {
		raw, _ := os.ReadFile(filepath.Join(want, "fake-claude.json"))
		var rec fakeClaudeRecord
		if json.Unmarshal(raw, &rec) == nil && rec.Pgid > 1 {
			syscall.Kill(-rec.Pgid, syscall.SIGKILL)
		}
	})
	eventually(t, "merger started", func() bool {
		var err error
		started, err = c.d.sessionStartGet(ctx, started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if started.Status == "failed" {
			t.Fatal(started.Error)
		}
		return started.Status == "started"
	})
	if _, err := os.Stat(filepath.Join(repo, "fake-claude.json")); !os.IsNotExist(err) {
		t.Fatal("the merger ran in the project folder")
	}
	// A second start reuses the same folder.
	dir, err := c.d.mergerFolder(ctx, repo, "garden")
	if err != nil || dir != want {
		t.Fatalf("second start: %q %v", dir, err)
	}
}
