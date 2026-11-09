package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The hand-back defect hint (defect accounting). A worker's diff may touch
// files that a recent merged change by another worker touched. The caller
// often fixes that work without naming `fixes`, so the notice names the
// candidate. It is a hint only: nothing here blocks the hand-back, and every
// failure leaves the notice as it was.

// defectHintGatewayWait bounds the gateway read at hand-back. defectHintWait
// bounds the whole hint, git calls included.
const (
	defectHintGatewayWait = 2 * time.Second
	defectHintWait        = 4 * time.Second
)

// overlapCandidate is one recent change with the files its merged commit
// changed.
type overlapCandidate struct {
	change protocol.RecentWorkerChange
	files  []string
}

// defectHint is the hand-back hint for w, or "". It reads up to five recent
// merged changes of the worker's project from the gateway and checks their
// file sets against the worker's diff. A worker that named `fixes` at its
// start gets none.
func (d *Daemon) defectHint(ctx context.Context, w *worker) string {
	d.wmu.Lock()
	fixes, dir, base, session, project, changeID, mainPath := w.fixes, w.worktree, w.base, w.sessionID, w.project, w.changeID, w.mainPath
	d.wmu.Unlock()
	if fixes != "" || dir == "" || session == "" || project == "" {
		return ""
	}
	hctx, hcancel := context.WithTimeout(ctx, defectHintWait)
	defer hcancel()
	files, err := changedFilesBetween(hctx, dir, base, "HEAD")
	if err != nil || len(files) == 0 {
		return ""
	}
	cctx, cancel := context.WithTimeout(hctx, defectHintGatewayWait)
	defer cancel()
	raw, err := d.call(cctx, protocol.FrameRecentWorkerChanges, protocol.RecentWorkerChangesRequest{
		Project: project, ExcludeSession: session, ExcludeChange: changeID, Limit: 5})
	if err != nil {
		return ""
	}
	var out protocol.RecentWorkerChangesResult
	if json.Unmarshal(raw, &out) != nil {
		return ""
	}
	var candidates []overlapCandidate
	for _, c := range out.Changes {
		if c.WorkerSession == session {
			continue
		}
		changed, err := commitFileSet(hctx, dir, c.MergedCommit)
		if err != nil && mainPath != "" {
			changed, err = commitFileSet(hctx, mainPath, c.MergedCommit)
		}
		if err != nil {
			continue
		}
		candidates = append(candidates, overlapCandidate{change: c, files: changed})
	}
	return fileOverlapHint(session, files, candidates)
}

// fileOverlapHint is the line for the first candidate whose files overlap the
// worker's, or "". A candidate by the worker itself is never named.
func fileOverlapHint(workerSession string, files []string, candidates []overlapCandidate) string {
	own := map[string]bool{}
	for _, f := range files {
		own[f] = true
	}
	for _, c := range candidates {
		if c.change.WorkerSession == workerSession {
			continue
		}
		overlap := false
		for _, f := range c.files {
			if own[f] {
				overlap = true
				break
			}
		}
		if !overlap {
			continue
		}
		worker := c.change.WorkerSession
		if worker == "" {
			worker = c.change.WorkerID
		}
		merged := time.Unix(c.change.MergedAt, 0).UTC().Format("2006-01-02")
		return fmt.Sprintf("This touches files from %s by %s (merged %s). If it fixes that work, record it: agent_worker_rate defect_found_later on %s, or submit with fixes=%s.",
			c.change.ChangeID, worker, merged, worker, c.change.ChangeID)
	}
	return ""
}

// changedFilesBetween is the set of files a git diff base..head changed. An
// empty or invalid base falls back to the files of head's own commit.
func changedFilesBetween(ctx context.Context, dir, base, head string) ([]string, error) {
	if !protocol.ValidCommit(base) {
		return commitFileSet(ctx, dir, head)
	}
	out, err := gitOut(ctx, dir, "diff", "--name-only", base, head)
	if err != nil {
		return nil, err
	}
	return splitFileLines(out), nil
}

// commitFileSet is the set of files a commit changed. One git call.
func commitFileSet(ctx context.Context, dir, commit string) ([]string, error) {
	if commit == "" {
		return nil, fmt.Errorf("no commit")
	}
	out, err := gitOut(ctx, dir, "show", "--format=", "--name-only", commit)
	if err != nil {
		return nil, err
	}
	return splitFileLines(out), nil
}

func splitFileLines(out string) []string {
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	sort.Strings(files)
	return files
}
