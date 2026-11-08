package daemon

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliveryReconcileRepo(t *testing.T) {
	for _, tc := range []struct {
		name         string
		merged, gone bool
		age          time.Duration
		want         string
	}{
		{"merged", true, false, time.Hour, "external"},
		{"unmerged", false, false, time.Hour, ""},
		{"branch gone", false, true, 8 * 24 * time.Hour, "abandoned"},
		{"young branch gone", false, true, time.Hour, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, wt := deliveryWorktree(t)
			head := gitHead(t, wt, "HEAD")
			base := gitHead(t, repo, "main")
			if tc.merged {
				gitIn(t, repo, "merge", "--no-ff", "feat/x", "-m", "merge")
			}
			remote := filepath.Join(t.TempDir(), "remote.git")
			if out, err := exec.Command("git", "clone", "--bare", repo, remote).CombinedOutput(); err != nil {
				t.Fatalf("clone: %s: %v", out, err)
			}
			gitIn(t, repo, "remote", "add", "origin", remote)
			gitIn(t, repo, "fetch", "origin")
			gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
			if tc.gone {
				gitIn(t, repo, "worktree", "remove", wt)
				gitIn(t, repo, "branch", "-D", "feat/x")
				gitIn(t, remote, "branch", "-D", "feat/x")
			}
			now := time.Now()
			c := protocol.Change{ID: "ch_test", HeadCommit: head, BaseCommit: base, Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: now.Add(-tc.age).Unix()}
			got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, now)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected reconciliation: %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != tc.want {
				t.Fatalf("want %s, got %+v", tc.want, got)
			}
			if tc.merged && got[0].MergedCommit != gitHead(t, repo, "main") {
				t.Fatalf("wrong merge commit: %+v", got)
			}
		})
	}
}

func TestDeliveryReconcileFetchesRemoteDefault(t *testing.T) {
	repo, wt := deliveryWorktree(t)
	head := gitHead(t, wt, "HEAD")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "clone", "--bare", repo, remote).CombinedOutput(); err != nil {
		t.Fatalf("clone: %s: %v", out, err)
	}
	gitIn(t, repo, "remote", "add", "origin", remote)
	gitIn(t, repo, "fetch", "origin")
	gitIn(t, remote, "update-ref", "refs/heads/main", head)
	c := protocol.Change{ID: "ch_test", HeadCommit: head, BaseCommit: gitHead(t, repo, "main"), Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	if err != nil || len(got) != 1 || got[0].Kind != "external" {
		t.Fatalf("fresh remote merge missed: %+v, %v", got, err)
	}
	if gitHead(t, repo, "main") == head {
		t.Fatal("local main was modified")
	}
}

func TestDeliveryReconcileKeepsRemoteBranchAndFetchFailures(t *testing.T) {
	repo, wt := deliveryWorktree(t)
	head := gitHead(t, wt, "HEAD")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "clone", "--bare", repo, remote).CombinedOutput(); err != nil {
		t.Fatalf("clone: %s: %v", out, err)
	}
	gitIn(t, repo, "remote", "add", "origin", remote)
	gitIn(t, repo, "fetch", "origin")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitIn(t, repo, "worktree", "remove", wt)
	gitIn(t, repo, "branch", "-D", "feat/x")
	c := protocol.Change{ID: "ch_test", HeadCommit: head, BaseCommit: gitHead(t, repo, "main"), Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Add(-8 * 24 * time.Hour).Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	if err != nil || len(got) != 0 {
		t.Fatalf("remote branch abandoned: %+v, %v", got, err)
	}
	gitIn(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	got, err = deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	if err == nil || len(got) != 0 {
		t.Fatalf("fetch error became observation: %+v, %v", got, err)
	}
}

func TestDeliveryReconcileRateAndMachine(t *testing.T) {
	n, f := startDeliveryNode(t)
	repo, wt := deliveryWorktree(t)
	head := gitHead(t, wt, "HEAD")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "clone", "--bare", repo, remote).CombinedOutput(); err != nil {
		t.Fatalf("clone: %s: %v", out, err)
	}
	gitIn(t, repo, "remote", "add", "origin", remote)
	gitIn(t, remote, "update-ref", "refs/heads/main", head)
	f.answer("query.projects", func(json.RawMessage) (any, error) {
		return protocol.ProjectsResult{Projects: []protocol.ProjectView{{Name: "demo", Path: repo, NodeID: n.d.NodeID()}}}, nil
	})
	f.answer(protocol.FrameChangeList, func(json.RawMessage) (any, error) {
		own := protocol.Change{ID: "ch_own", Project: "demo", Machine: n.d.NodeID(), Branch: "feat/x", HeadCommit: head, BaseCommit: gitHead(t, repo, "main"), Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
		other := own
		other.ID = "ch_other"
		other.Machine = "other-node"
		return protocol.ChangeListResult{Changes: []protocol.ChangeView{{Change: own}, {Change: other}}}, nil
	})
	f.answer(protocol.FrameChangeReconcile, func(raw json.RawMessage) (any, error) {
		var q protocol.ChangeReconcileRequest
		if err := json.Unmarshal(raw, &q); err != nil {
			return nil, err
		}
		if q.ChangeID != "ch_own" {
			t.Errorf("reconciled another machine's change: %+v", q)
		}
		return protocol.ChangeView{}, nil
	})
	checked := make(map[string]time.Time)
	n.d.reconcileDelivery(context.Background(), checked)
	n.d.reconcileDelivery(context.Background(), checked)
	if got := len(f.frames(protocol.FrameChangeReconcile)); got != 1 {
		t.Fatalf("got %d observations, want one within ten minutes", got)
	}
	if got := len(f.frames(protocol.FrameChangeList)); got != 1 {
		t.Fatalf("got %d list calls, want one within ten minutes", got)
	}
}

func TestDeliveryReconcileNoOwnCommitsStaysOpen(t *testing.T) {
	for _, kind := range []string{"head equals base", "head precedes base", "unknown base"} {
		t.Run(kind, func(t *testing.T) {
			repo, wt := deliveryWorktree(t)
			base, tip := gitHead(t, repo, "main"), gitHead(t, wt, "HEAD")
			gitIn(t, repo, "merge", "--ff-only", "feat/x")
			remote := filepath.Join(t.TempDir(), "remote.git")
			if out, err := exec.Command("git", "clone", "--bare", repo, remote).CombinedOutput(); err != nil {
				t.Fatalf("clone: %s: %v", out, err)
			}
			gitIn(t, repo, "remote", "add", "origin", remote)
			c := protocol.Change{ID: "ch_empty", HeadCommit: base, BaseCommit: base, Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
			if kind == "head precedes base" {
				c.BaseCommit = tip
			}
			if kind == "unknown base" {
				c.BaseCommit = ""
			}
			got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 {
				t.Fatalf("change without own commits would close: %+v", got)
			}
		})
	}
}
