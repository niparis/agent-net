package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/repoinfo"
)

const deliveryReconcileEvery = 10 * time.Minute

// deliveryGH runs gh in a repository for the GitHub merge check. Tests
// replace it so no test ever calls the real gh. The daemon uses it at most
// once per repository per pass, plus one pr view per newly matched PR.
var deliveryGH = func(ctx context.Context, folder string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = folder
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return nil, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// githubMergedPR is the part of a merged pull request the reconcile needs.
type githubMergedPR struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	HeadRefOID  string `json:"headRefOid"`
	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
	MergedAt time.Time `json:"mergedAt"`
	URL      string    `json:"url"`
}

// githubCheck is one entry of a pull request's statusCheckRollup. Check runs
// use Status and Conclusion; older commit statuses use State.
type githubCheck struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// githubMergedPRs asks GitHub once for the repository's merged pull requests.
func githubMergedPRs(ctx context.Context, folder string) ([]githubMergedPR, error) {
	out, err := deliveryGH(ctx, folder, "pr", "list", "--state", "merged", "--limit", "100",
		"--json", "number,headRefName,headRefOid,mergeCommit,mergedAt,url")
	if err != nil {
		return nil, err
	}
	var prs []githubMergedPR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	return prs, nil
}

// prHoldsHead reports whether the merged pull request contains the change's
// head: its head commit is the change head, or a later commit of its branch
// that has the change head as an ancestor (the worker pushed more after its
// hand-back). A commit git does not have is not proof.
func prHoldsHead(ctx context.Context, folder string, pr githubMergedPR, head string) bool {
	if pr.HeadRefOID == head {
		return true
	}
	if !protocol.ValidCommit(pr.HeadRefOID) {
		return false
	}
	_, err := gitOut(ctx, folder, "merge-base", "--is-ancestor", head, pr.HeadRefOID)
	return err == nil
}

// githubChecksPassed reports whether every check on the pull request passed.
// No checks at all is not a pass: a test step needs evidence.
func githubChecksPassed(ctx context.Context, folder string, number int) (bool, error) {
	out, err := deliveryGH(ctx, folder, "pr", "view", strconv.Itoa(number), "--json", "statusCheckRollup")
	if err != nil {
		return false, err
	}
	var v struct {
		StatusCheckRollup []githubCheck `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return false, fmt.Errorf("gh pr view %d: %w", number, err)
	}
	if len(v.StatusCheckRollup) == 0 {
		return false, nil
	}
	for _, c := range v.StatusCheckRollup {
		if c.Status != "" {
			if c.Status != "COMPLETED" {
				return false, nil
			}
			switch c.Conclusion {
			case "SUCCESS", "NEUTRAL", "SKIPPED":
			default:
				return false, nil
			}
			continue
		}
		if c.State != "SUCCESS" {
			return false, nil
		}
	}
	return true, nil
}

// isGitHubRemote reports whether origin is on github.com. Any other remote
// keeps the git-only reconcile and needs no gh call.
func isGitHubRemote(remote string) bool {
	return strings.Contains(strings.ToLower(remote), "github.com")
}

// errNotGitHubRemote says the repo is not on GitHub, so no pull request can
// be asked about. The caller logs it once per repo and moves on.
type errNotGitHubRemote struct{ remote string }

func (e errNotGitHubRemote) Error() string {
	return "origin " + e.remote + " is not a GitHub remote; PR merge check skipped"
}

// reconcileLogOnce reports whether this repo path was not logged yet in this
// daemon run.
func (d *Daemon) reconcileLogOnce(path string) bool {
	d.reconcileLogMu.Lock()
	defer d.reconcileLogMu.Unlock()
	if d.reconcileLogs == nil {
		d.reconcileLogs = make(map[string]bool)
	}
	if d.reconcileLogs[path] {
		return false
	}
	d.reconcileLogs[path] = true
	return true
}

// One serial loop owns the per-repository deadline, including failed attempts.
// No session is needed: the daemon observes only its own machine's changes.
func (d *Daemon) deliveryReconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	checked := make(map[string]time.Time)
	for {
		d.reconcileDelivery(ctx, checked)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) reconcileDelivery(ctx context.Context, checked map[string]time.Time) {
	var projects protocol.ProjectsResult
	if err := d.deliveryCall(ctx, "query.projects", protocol.QueryProjects{}, &projects); err != nil {
		return
	}
	for _, p := range projects.Projects {
		if p.NodeID != d.cfg.NodeID || p.Path == "" {
			continue
		}
		now := time.Now()
		if last, ok := checked[p.Path]; ok && now.Sub(last) < deliveryReconcileEvery {
			continue
		}
		checked[p.Path] = now
		var changes []protocol.ChangeView
		q := protocol.ChangeListRequest{Project: p.Name, State: protocol.ChangeOpen, Limit: 100}
		for {
			var page protocol.ChangeListResult
			if err := d.deliveryCall(ctx, protocol.FrameChangeList, q, &page); err != nil {
				return
			}
			for _, v := range page.Changes {
				if v.Change.Machine == d.cfg.NodeID {
					changes = append(changes, v)
				}
			}
			if page.Next == "" {
				break
			}
			q.After = page.Next
		}
		if len(changes) == 0 {
			continue
		}
		observations, err := deliveryReconcileRepo(ctx, p.Path, changes, now)
		if err != nil {
			// A GitHub lookup that failed leaves the git observations made
			// in the same pass standing. A repo that is not on GitHub is
			// logged once per daemon run; a gh failure once per pass.
			var notGitHub errNotGitHubRemote
			if errors.As(err, &notGitHub) {
				if d.reconcileLogOnce(p.Path) {
					d.Logf("delivery reconcile %s: %v", p.Name, err)
				}
			} else {
				d.Logf("delivery reconcile %s: %v", p.Name, err)
			}
		}
		for _, observation := range observations {
			var out protocol.ChangeView
			if err := d.deliveryCall(ctx, protocol.FrameChangeReconcile, observation, &out); err != nil {
				d.Logf("delivery reconcile %s: %v", observation.ChangeID, err)
			}
		}
	}
}

// deliveryReconcileRepo reads remote truth, never local default-branch state.
// A failed fetch or branch listing leaves every change untouched.
func deliveryReconcileRepo(ctx context.Context, folder string, changes []protocol.ChangeView, now time.Time) ([]protocol.ChangeReconcileRequest, error) {
	info, err := repoinfo.New().Inspect(folder)
	if err != nil {
		return nil, err
	}
	if info.Remote == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if info.DefaultBranch == "" {
		// Some clones have no origin/HEAD. Ask origin without setting a
		// symbolic ref or guessing a branch from the owner's checkout.
		head, err := gitOut(ctx, info.Path, "ls-remote", "--symref", "origin", "HEAD")
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(head, "\n") {
			parts := strings.Fields(line)
			if len(parts) == 3 && parts[0] == "ref:" && parts[2] == "HEAD" && strings.HasPrefix(parts[1], "refs/heads/") {
				info.DefaultBranch = strings.TrimPrefix(parts[1], "refs/heads/")
				break
			}
		}
		if info.DefaultBranch == "" {
			return nil, nil
		}
	}
	ref := "refs/remotes/origin/" + info.DefaultBranch
	if _, err := gitOut(ctx, info.Path, "-c", "core.hooksPath=/dev/null", "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "origin", "+refs/heads/"+info.DefaultBranch+":"+ref); err != nil {
		return nil, fmt.Errorf("fetch default branch: %w", err)
	}
	tip, err := gitOut(ctx, info.Path, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return nil, err
	}
	var out []protocol.ChangeReconcileRequest
	// GitHub is asked at most once per repository per pass. Its result is
	// shared by every change; one pr view is made per matched PR.
	var (
		githubPRs    []githubMergedPR
		githubLoaded bool
		githubErr    error
		githubChecks = map[int]bool{}
	)
	// Load the remote's actual branch names, rather than stale tracking refs.
	var remoteHeads map[string]bool
	// branchAbandoned reports whether a change with no step report, older than
	// seven days, has its branch absent locally and on the remote.
	branchAbandoned := func(v protocol.ChangeView) (bool, error) {
		c := v.Change
		if c.Branch == "" || now.Sub(time.Unix(c.CreatedAt, 0)) <= 7*24*time.Hour || len(v.Evidence) > 0 {
			return false, nil
		}
		for _, step := range v.Steps {
			if step.Evidence != nil {
				return false, nil
			}
		}
		// rev-parse reports a missing ref with exit 128, also used for other
		// errors. Enumerating refs lets errors remain errors, never absence.
		local, err := gitOut(ctx, info.Path, "for-each-ref", "--format=%(refname)", "refs/heads/")
		if err != nil {
			return false, err
		}
		for _, ref := range strings.Fields(local) {
			if ref == "refs/heads/"+c.Branch {
				return false, nil
			}
		}
		if remoteHeads == nil {
			heads, err := gitOut(ctx, info.Path, "ls-remote", "--heads", "origin")
			if err != nil {
				return false, err
			}
			remoteHeads = make(map[string]bool)
			for _, line := range strings.Split(heads, "\n") {
				parts := strings.Fields(line)
				if len(parts) == 2 {
					remoteHeads[parts[1]] = true
				}
			}
		}
		return !remoteHeads["refs/heads/"+c.Branch], nil
	}
	for _, v := range changes {
		c := v.Change
		if c.State != protocol.ChangeOpen || c.Uncommitted || !protocol.ValidCommit(c.HeadCommit) {
			continue
		}
		q := protocol.ChangeReconcileRequest{ChangeID: c.ID, Revision: c.Revision, HeadCommit: c.HeadCommit}
		// A head equal to its base records no work: a merge of the base says
		// nothing about this change, and a PR cannot be tied to it. The caller
		// hears about it once; an old change with its branch gone still closes
		// as abandoned below.
		noWork := c.HeadCommit == c.BaseCommit
		if noWork {
			q.Kind = protocol.ReconcileNoWork
		}
		// Require commits beyond a known base; git errors do not prove that
		// the head is outside its history.
		if !noWork && protocol.ValidCommit(c.BaseCommit) {
			_, err := gitOut(ctx, info.Path, "merge-base", "--is-ancestor", c.HeadCommit, c.BaseCommit)
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				if _, err := gitOut(ctx, info.Path, "merge-base", "--is-ancestor", c.HeadCommit, tip); err == nil {
					q.Kind, q.MergedCommit, q.Proof = protocol.ReconcileExternal, tip, protocol.MergeProofAncestor
					out = append(out, q)
					continue
				}
				// A squash or rebase merge makes new commits, so the head is
				// not an ancestor of the tip. Ask GitHub for the merged PR
				// that holds this head, whatever its branch is now called:
				// leads rename branches before they push. When GitHub cannot
				// say, look for the squash or cherry-pick shape in git.
				{
					if !githubLoaded {
						githubLoaded = true
						if !isGitHubRemote(info.Remote) {
							githubErr = errNotGitHubRemote{remote: info.Remote}
						} else if prs, err := githubMergedPRs(ctx, info.Path); err != nil {
							githubErr = err
						} else {
							githubPRs = prs
						}
					}
					matched := false
					if githubErr == nil {
						for _, pr := range githubPRs {
							if !protocol.ValidCommit(pr.MergeCommit.OID) || !prHoldsHead(ctx, info.Path, pr, c.HeadCommit) {
								continue
							}
							passed, ok := githubChecks[pr.Number]
							if !ok {
								passed, err = githubChecksPassed(ctx, info.Path, pr.Number)
								if err != nil {
									githubErr = err
									break
								}
								githubChecks[pr.Number] = passed
							}
							q.Kind, q.MergedCommit = protocol.ReconcileExternal, pr.MergeCommit.OID
							q.PRNumber, q.PRURL, q.PRChecksPassed = pr.Number, pr.URL, passed
							out = append(out, q)
							matched = true
							break
						}
					}
					if matched {
						continue
					}
				}
				if cleanupCherryMerged(ctx, info.Path, c.HeadCommit, ref) {
					q.Kind, q.MergedCommit, q.Proof = protocol.ReconcileExternal, tip, protocol.MergeProofCherry
					out = append(out, q)
					continue
				}
				if squash := cleanupSquashCommit(ctx, info.Path, c.HeadCommit, ref); squash != "" {
					q.Kind, q.MergedCommit, q.Proof = protocol.ReconcileExternal, squash, protocol.MergeProofSquash
					out = append(out, q)
					continue
				}
			}
		}
		gone, err := branchAbandoned(v)
		if err != nil {
			return nil, err
		}
		switch {
		case gone:
			q.Kind = protocol.ReconcileAbandoned
			out = append(out, q)
		case noWork:
			out = append(out, q)
		}
	}
	// A GitHub lookup error leaves the git observations intact and is logged
	// once by the caller. It never fails the loop.
	return out, githubErr
}
