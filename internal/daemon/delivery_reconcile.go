package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/repoinfo"
)

const deliveryReconcileEvery = 10 * time.Minute

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
			d.Logf("delivery reconcile %s: %v", p.Name, err)
			continue
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
	// Load the remote's actual branch names, rather than stale tracking refs.
	var remoteHeads map[string]bool
	for _, v := range changes {
		c := v.Change
		if c.State != protocol.ChangeOpen || c.Uncommitted || !protocol.ValidCommit(c.HeadCommit) {
			continue
		}
		q := protocol.ChangeReconcileRequest{ChangeID: c.ID, Revision: c.Revision, HeadCommit: c.HeadCommit}
		if _, err := gitOut(ctx, info.Path, "merge-base", "--is-ancestor", c.HeadCommit, tip); err == nil {
			q.Kind, q.MergedCommit = "external", tip
			out = append(out, q)
			continue
		}
		if c.Branch == "" || now.Sub(time.Unix(c.CreatedAt, 0)) <= 7*24*time.Hour || len(v.Evidence) > 0 {
			continue
		}
		reported := false
		for _, step := range v.Steps {
			if step.Evidence != nil {
				reported = true
				break
			}
		}
		if reported {
			continue
		}
		// rev-parse reports a missing ref with exit 128, also used for other
		// errors. Enumerating refs lets errors remain errors, never absence.
		local, err := gitOut(ctx, info.Path, "for-each-ref", "--format=%(refname)", "refs/heads/")
		if err != nil {
			return nil, err
		}
		exists := false
		for _, ref := range strings.Fields(local) {
			if ref == "refs/heads/"+c.Branch {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		if remoteHeads == nil {
			heads, err := gitOut(ctx, info.Path, "ls-remote", "--heads", "origin")
			if err != nil {
				return nil, err
			}
			remoteHeads = make(map[string]bool)
			for _, line := range strings.Split(heads, "\n") {
				parts := strings.Fields(line)
				if len(parts) == 2 {
					remoteHeads[parts[1]] = true
				}
			}
		}
		if !remoteHeads["refs/heads/"+c.Branch] {
			q.Kind = "abandoned"
			out = append(out, q)
		}
	}
	return out, nil
}
