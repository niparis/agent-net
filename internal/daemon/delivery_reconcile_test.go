package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestDeliveryReconcileRepo(t *testing.T) {
	t.Parallel()
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
			ignoreNotGitHub(t, err)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unexpected reconciliation: %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != tc.want {
				t.Fatalf("want %s, got %+v", tc.want, got)
			}
			if tc.merged && (got[0].MergedCommit != gitHead(t, repo, "main") || got[0].Proof != protocol.MergeProofAncestor) {
				t.Fatalf("wrong merge commit or proof: %+v", got)
			}
		})
	}
}

func TestDeliveryReconcileFetchesRemoteDefault(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	ignoreNotGitHub(t, err)
	if len(got) != 0 {
		t.Fatalf("remote branch abandoned: %+v, %v", got, err)
	}
	gitIn(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	got, err = deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	if err == nil || len(got) != 0 {
		t.Fatalf("fetch error became observation: %+v, %v", got, err)
	}
}

func TestDeliveryReconcileRateAndMachine(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
			ignoreNotGitHub(t, err)
			for _, q := range got {
				if q.Kind != protocol.ReconcileNoWork {
					t.Fatalf("change without own commits would close: %+v", got)
				}
			}
			// Only a head equal to its base is a recorded "no work" change.
			if want := 0; kind == "head equals base" {
				want = 1
				if len(got) != want || got[0].ChangeID != "ch_empty" || got[0].HeadCommit != base {
					t.Fatalf("no-work change not reported once: %+v", got)
				}
			} else if len(got) != want {
				t.Fatalf("unexpected observation: %+v", got)
			}
		})
	}
}

// A head equal to its base is never merged, even when GitHub lists a merged
// pull request for the branch: the work was not recorded, so nothing ties the
// PR to this change. gh is not asked at all.
func TestDeliveryReconcileNoWorkIgnoresMergedPR(t *testing.T) {
	repo, head, squash := githubDeliveryRepo(t)
	calls := fakeGH(t, prListJSON(81, "feat/x", head, squash, "https://github.com/example/demo/pull/81"), checksPassedJSON)
	base := gitHead(t, repo, "origin/main")
	c := protocol.Change{ID: "ch_nowork", HeadCommit: base, BaseCommit: base, Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != protocol.ReconcileNoWork || got[0].MergedCommit != "" || got[0].PRNumber != 0 {
		t.Fatalf("head equal to base was treated as merged: %+v", got)
	}
	if len(*calls) != 0 {
		t.Fatalf("gh called for a change with no work: %v", *calls)
	}
}

// A change with no commits beyond its base whose branch is long gone is still
// closed as abandoned, and is not also reported as "no work".
func TestDeliveryReconcileAbandonedNoWorkIsNotReported(t *testing.T) {
	repo, wt := deliveryWorktree(t)
	base := gitHead(t, repo, "main")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "clone", "--bare", repo, remote).CombinedOutput(); err != nil {
		t.Fatalf("clone: %s: %v", out, err)
	}
	gitIn(t, repo, "remote", "add", "origin", remote)
	gitIn(t, repo, "fetch", "origin")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitIn(t, repo, "worktree", "remove", wt)
	gitIn(t, repo, "branch", "-D", "feat/x")
	gitIn(t, remote, "branch", "-D", "feat/x")
	c := protocol.Change{ID: "ch_gone", HeadCommit: base, BaseCommit: base, Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Add(-8 * 24 * time.Hour).Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	ignoreNotGitHub(t, err)
	if len(got) != 1 || got[0].Kind != protocol.ReconcileAbandoned {
		t.Fatalf("want one abandoned observation: %+v", got)
	}
}

// fakeGH replaces the gh runner for one test and records the arguments of
// every call. list and view are the JSON gh returns.
func fakeGH(t *testing.T, list, view string) *[]string {
	t.Helper()
	old := deliveryGH
	t.Cleanup(func() { deliveryGH = old })
	calls := &[]string{}
	deliveryGH = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		*calls = append(*calls, strings.Join(args, " "))
		switch {
		case len(args) > 1 && args[0] == "pr" && args[1] == "list":
			return []byte(list), nil
		case len(args) > 1 && args[0] == "pr" && args[1] == "view":
			return []byte(view), nil
		}
		return nil, fmt.Errorf("unexpected gh call: %v", args)
	}
	return calls
}

// ignoreNotGitHub fails on any error except the one a non-GitHub origin
// gives. The git-only tests use a local bare remote.
func ignoreNotGitHub(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	var notGitHub errNotGitHubRemote
	if errors.As(err, &notGitHub) {
		return
	}
	t.Fatal(err)
}

// githubDeliveryRepo makes demo with main, a feat/x worktree, and a squash
// merge of feat/x on main: the new commit has a different hash, so the branch
// head is not an ancestor of the main tip. Origin's URL is a GitHub URL that
// git rewrites to a local bare clone, so the repo looks like GitHub to the
// reconcile and no network is used. It returns the repo folder, the branch
// head and the squash commit.
func githubDeliveryRepo(t *testing.T) (repo, head, squash string) {
	t.Helper()
	repo, wt := deliveryWorktree(t)
	head = gitHead(t, wt, "HEAD")
	gitIn(t, repo, "merge", "--squash", "feat/x")
	gitIn(t, repo, "commit", "-q", "-m", "squash feat/x")
	squash = gitHead(t, repo, "HEAD")
	remote := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "clone", "--bare", repo, remote).CombinedOutput(); err != nil {
		t.Fatalf("clone: %s: %v", out, err)
	}
	const githubURL = "https://github.com/example/demo.git"
	gitIn(t, repo, "remote", "add", "origin", githubURL)
	gitIn(t, repo, "config", "url."+remote+".insteadOf", githubURL)
	gitIn(t, repo, "fetch", "origin")
	gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	return repo, head, squash
}

func prListJSON(number int, branch, head, merge, url string) string {
	return fmt.Sprintf(`[{"number":%d,"headRefName":%q,"headRefOid":%q,"mergeCommit":{"oid":%q},"mergedAt":"2026-11-01T10:00:00Z","url":%q}]`,
		number, branch, head, merge, url)
}

const checksPassedJSON = `{"statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"},{"state":"SUCCESS"}]}`

func TestDeliveryReconcileSquashMergeByPR(t *testing.T) {
	repo, head, squash := githubDeliveryRepo(t)
	url := "https://github.com/example/demo/pull/77"
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	calls := fakeGH(t, prListJSON(77, "feat/x", head, squash, url), checksPassedJSON)
	c := protocol.Change{ID: "ch_pr", HeadCommit: head, BaseCommit: gitHead(t, repo, "origin/main"), Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("squash merge through a PR was missed: %+v", got)
	}
	q := got[0]
	if q.Kind != "external" || q.MergedCommit != squash || q.PRNumber != 77 || q.PRURL != url || !q.PRChecksPassed {
		t.Fatalf("wrong PR observation: %+v", q)
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[0], "pr list") || !strings.Contains((*calls)[1], "pr view 77") {
		t.Fatalf("gh calls: %v", *calls)
	}
}

func TestDeliveryReconcilePRHeadMustMatch(t *testing.T) {
	repo, head, squash := githubDeliveryRepo(t)
	// The PR was merged from an older head, not this revision.
	older := strings.Repeat("e", 40)
	fakeGH(t, prListJSON(78, "feat/x", older, squash, "https://github.com/example/demo/pull/78"), checksPassedJSON)
	c := protocol.Change{ID: "ch_old", HeadCommit: head, BaseCommit: gitHead(t, repo, "origin/main"), Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	// Git still sees the single-commit squash, but not through the PR.
	if err != nil || len(got) != 1 || got[0].PRNumber != 0 || got[0].Proof != protocol.MergeProofCherry {
		t.Fatalf("PR merged from an older head matched: %+v, %v", got, err)
	}
}

func TestDeliveryReconcileGHErrorLeavesChangeOpen(t *testing.T) {
	repo, head, _ := githubDeliveryRepo(t)
	old := deliveryGH
	t.Cleanup(func() { deliveryGH = old })
	deliveryGH = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("gh: not logged in")
	}
	c := protocol.Change{ID: "ch_err", HeadCommit: head, BaseCommit: gitHead(t, repo, "origin/main"), Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	// The git-only proof stands; nothing is claimed from GitHub.
	if len(got) != 1 || got[0].PRNumber != 0 || got[0].Proof == "" {
		t.Fatalf("gh error changed the git observation: %+v", got)
	}
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("gh error was not reported for one log: %v", err)
	}
}

func TestDeliveryReconcileGHCallsOncePerRepo(t *testing.T) {
	repo, head, squash := githubDeliveryRepo(t)
	calls := fakeGH(t, prListJSON(79, "feat/x", head, squash, "https://github.com/example/demo/pull/79"), checksPassedJSON)
	base := gitHead(t, repo, "origin/main")
	unmatched := protocol.Change{ID: "ch_none", HeadCommit: strings.Repeat("e", 40), BaseCommit: base, Branch: "feat/other", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	matched := protocol.Change{ID: "ch_one", HeadCommit: head, BaseCommit: base, Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: unmatched}, {Change: matched}}, time.Now())
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	list := 0
	for _, call := range *calls {
		if strings.Contains(call, "pr list") {
			list++
		}
	}
	if list != 1 {
		t.Fatalf("gh pr list called %d times: %v", list, *calls)
	}
}

func TestDeliveryReconcilePRChecksNotPassed(t *testing.T) {
	repo, head, squash := githubDeliveryRepo(t)
	calls := fakeGH(t, prListJSON(80, "feat/x", head, squash, "https://github.com/example/demo/pull/80"),
		`{"statusCheckRollup":[{"status":"COMPLETED","conclusion":"FAILURE"}]}`)
	c := protocol.Change{ID: "ch_fail", HeadCommit: head, BaseCommit: gitHead(t, repo, "origin/main"), Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	if err != nil || len(got) != 1 || got[0].PRChecksPassed {
		t.Fatalf("failed checks recorded as passed: %+v, %v", got, err)
	}
	if len(*calls) != 2 {
		t.Fatalf("gh calls: %v", *calls)
	}
}

// A merge without a GitHub PR record leaves two shapes on the default branch.
// A squash makes one commit with the branch's tree; a cherry-pick or rebase
// copies each commit. Neither makes the head an ancestor of the tip.
func TestDeliveryReconcileLocalProofs(t *testing.T) {
	for _, tc := range []struct {
		name, proof string
		land        func(t *testing.T, repo string)
	}{
		{"squash", protocol.MergeProofSquash, func(t *testing.T, repo string) {
			gitIn(t, repo, "merge", "--squash", "feat/x")
			gitIn(t, repo, "commit", "-q", "-m", "squash feat/x")
		}},
		{"cherry", protocol.MergeProofCherry, func(t *testing.T, repo string) {
			gitIn(t, repo, "cherry-pick", "feat/x")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, wt := deliveryWorktree(t)
			base := gitHead(t, repo, "main")
			if tc.proof == protocol.MergeProofSquash {
				// Two commits squashed into one are not patch-equivalent one by one.
				if err := os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				gitIn(t, wt, "add", "c.txt")
				gitIn(t, wt, "commit", "-q", "-m", "second")
			}
			head := gitHead(t, wt, "HEAD")
			// Main moves on first, so a cherry-pick gets a new parent and a new hash.
			if err := os.WriteFile(filepath.Join(repo, "other.txt"), []byte("other\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitIn(t, repo, "add", "other.txt")
			gitIn(t, repo, "commit", "-q", "-m", "other work")
			tc.land(t, repo)
			remote := filepath.Join(t.TempDir(), "remote.git")
			if out, err := exec.Command("git", "clone", "--bare", repo, remote).CombinedOutput(); err != nil {
				t.Fatalf("clone: %s: %v", out, err)
			}
			gitIn(t, repo, "remote", "add", "origin", remote)
			gitIn(t, repo, "fetch", "origin")
			gitIn(t, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
			c := protocol.Change{ID: "ch_local", HeadCommit: head, BaseCommit: base, Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
			got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
			ignoreNotGitHub(t, err)
			if len(got) != 1 || got[0].Kind != protocol.ReconcileExternal || got[0].Proof != tc.proof || got[0].MergedCommit != gitHead(t, repo, "main") || got[0].PRNumber != 0 {
				t.Fatalf("%s merge not proven: %+v", tc.name, got)
			}
		})
	}
}

// A worker that pushed more commits to the PR branch after its hand-back left
// the change at an older head. The merged PR still contains that head.
func TestDeliveryReconcilePRWithNewerHeadMatches(t *testing.T) {
	repo, head, squash := githubDeliveryRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "demo-feat")
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, wt, "add", "b.txt")
	gitIn(t, wt, "commit", "-q", "-m", "more")
	newer := gitHead(t, wt, "HEAD")
	fakeGH(t, prListJSON(82, "feat/x", newer, squash, "https://github.com/example/demo/pull/82"), checksPassedJSON)
	c := protocol.Change{ID: "ch_newer", HeadCommit: head, BaseCommit: gitHead(t, repo, "origin/main~1"), Branch: "feat/x", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PRNumber != 82 || got[0].MergedCommit != squash {
		t.Fatalf("PR merged from a newer head was missed: %+v", got)
	}
}

// Leads rename a branch before they push it, so the PR's head branch is not
// the branch agent-net recorded. The head commit still ties the PR to the change.
func TestDeliveryReconcilePRWithRenamedBranchMatches(t *testing.T) {
	repo, head, squash := githubDeliveryRepo(t)
	fakeGH(t, prListJSON(83, "chore/bump-0.8.31", head, squash, "https://github.com/example/demo/pull/83"), checksPassedJSON)
	c := protocol.Change{ID: "ch_renamed", HeadCommit: head, BaseCommit: gitHead(t, repo, "origin/main~1"), Branch: "agent-net/w_abc", Revision: 1, State: protocol.ChangeOpen, CreatedAt: time.Now().Unix()}
	got, err := deliveryReconcileRepo(context.Background(), repo, []protocol.ChangeView{{Change: c}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PRNumber != 83 {
		t.Fatalf("PR from a renamed branch was missed: %+v", got)
	}
}
