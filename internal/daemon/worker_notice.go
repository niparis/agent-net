package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Worker hand-back, completion notices and worktrees (decision 0002, W10, W11
// and W13).

// ---- worktrees ----

// git runs a git command in dir and returns its output.
func gitOut(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// isGitRepo tells whether dir is inside a git work tree with a commit, so that
// a worktree can be made from it.
func isGitRepo(ctx context.Context, dir string) bool {
	_, err := gitOut(ctx, dir, "rev-parse", "--verify", "HEAD")
	return err == nil
}

// createWorktree makes the worker's own worktree of its project, on a new
// branch, in a folder the daemon manages (not in the user's checkout). The
// worker then runs in it.
func (d *Daemon) createWorktree(ctx context.Context, w *worker) error {
	main := w.path
	base, err := gitOut(ctx, main, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("cannot make a worktree: %s is not a git repository with a commit (%v)", main, err)
	}
	dir := filepath.Join(d.cfg.StateDir, "worktrees", w.id)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	branch := protocol.WorktreeBranch(w.id)
	if _, err := gitOut(ctx, main, "worktree", "add", dir, "-b", branch); err != nil {
		return fmt.Errorf("cannot make a worktree: %w", err)
	}
	d.wmu.Lock()
	w.mainPath, w.path, w.worktree, w.branch, w.base = main, dir, dir, branch, base
	d.wmu.Unlock()
	return nil
}

// worktreeFacts returns git diff --stat of the worktree against the commit it
// started from, with the untracked files, and whether there is anything.
func (d *Daemon) worktreeFacts(w *worker) (stat string, changed bool) {
	d.wmu.Lock()
	dir, base := w.worktree, w.base
	d.wmu.Unlock()
	if dir == "" {
		return "", false
	}
	ctx := context.Background()
	var parts []string
	if s, err := gitOut(ctx, dir, "diff", "--stat", base); err == nil && s != "" {
		parts = append(parts, s)
	}
	if u, err := gitOut(ctx, dir, "ls-files", "--others", "--exclude-standard"); err == nil && u != "" {
		files := strings.Split(u, "\n")
		more := ""
		if len(files) > 20 {
			files, more = files[:20], fmt.Sprintf(" and %d more", len(files)-20)
		}
		parts = append(parts, "untracked: "+strings.Join(files, ", ")+more)
	}
	if n, err := gitOut(ctx, dir, "rev-list", "--count", base+"..HEAD"); err == nil && n != "0" && n != "" && len(parts) == 0 {
		parts = append(parts, n+" commit(s) with no file changes")
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

// finishWorktree keeps the worktree if the worker changed anything and removes
// it (and its branch) if not.
func (d *Daemon) finishWorktree(w *worker) {
	d.wmu.Lock()
	dir, main, branch := w.worktree, w.mainPath, w.branch
	d.wmu.Unlock()
	if dir == "" {
		return
	}
	if _, changed := d.worktreeFacts(w); changed {
		d.Logf("worker.worktree kept worker=%s path=%s branch=%s", w.id, dir, branch)
		return
	}
	ctx := context.Background()
	if _, err := gitOut(ctx, main, "worktree", "remove", "--force", dir); err != nil {
		d.Logf("worker %s: %v", w.id, err)
		return
	}
	gitOut(ctx, main, "branch", "-D", branch)
	d.Logf("worker.worktree removed worker=%s path=%s (no changes)", w.id, dir)
}

// ---- stats from the harness ----

// workerStats is what the harness reports about a worker's run.
type workerStats struct {
	Tokens string
	Cost   string
	Model  string
}

// workerStats asks the launcher, with a short time limit. It is empty when the
// harness cannot say (or the worker was adopted after a restart).
func (d *Daemon) workerStats(w *worker) workerStats {
	ln, ok := d.launchers[w.harness].(interface {
		stats(ctx context.Context, d *Daemon, w *worker) workerStats
	})
	if !ok {
		return workerStats{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return ln.stats(ctx, d, w)
}

// tokenText formats token counts, or "" when there are none.
func tokenText(in, out, reasoning, cacheRead, cacheWrite float64) string {
	if in+out+reasoning+cacheRead+cacheWrite == 0 {
		return ""
	}
	s := fmt.Sprintf("%.0f in, %.0f out", in, out)
	if reasoning > 0 {
		s += fmt.Sprintf(", %.0f reasoning", reasoning)
	}
	if cacheRead+cacheWrite > 0 {
		s += fmt.Sprintf(", %.0f cache read, %.0f cache write", cacheRead, cacheWrite)
	}
	return s
}

// ---- notices ----

// notice builds the facts of a completion notice.
func (d *Daemon) notice(w *worker, status, note string, st workerStats) protocol.WorkerNotice {
	d.wmu.Lock()
	sid, harness, model, project, wt, br, started := w.sessionID, w.harness, w.model, w.project, w.worktree, w.branch, w.startedAt
	d.wmu.Unlock()
	if model == "" {
		model = st.Model
	}
	n := protocol.WorkerNotice{Status: status, Note: note, Session: sid, Harness: harness, Model: model, Project: project,
		Host: d.cfg.Hostname, RunTime: time.Since(started), Tokens: st.Tokens, Cost: st.Cost, Worktree: wt, Branch: br}
	n.DiffStat, _ = d.worktreeFacts(w)
	return n
}

// sendNotice sends the notice, with the report below it if there is one, to
// the worker's owner. It is a reply to the last request of the owner, so the
// caller sees it in that conversation. The sender is the worker's session.
func (d *Daemon) sendNotice(ctx context.Context, w *worker, n protocol.WorkerNotice, report string) (json.RawMessage, error) {
	return d.sendNoticeTo(ctx, w, n, report, "", "")
}

// sendNoticeTo is sendNotice to another session than the owner, as a reply to
// another message than the owner's last request. Empty to and replyTo mean the
// owner and its last request.
func (d *Daemon) sendNoticeTo(ctx context.Context, w *worker, n protocol.WorkerNotice, report, to, replyTo string) (json.RawMessage, error) {
	d.wmu.Lock()
	sid, owner, parent := w.sessionID, w.owner, w.taskMsg
	d.wmu.Unlock()
	if to != "" {
		owner, parent = to, replyTo
	}
	if sid == "" {
		return nil, errors.New("the worker has no session")
	}
	if len(report) > 48*1024 {
		report = strings.ToValidUTF8(report[:48*1024], "") + "\n[report cut at 48 KB]"
	}
	p := protocol.MsgSend{Sender: sid, Recipient: owner, Type: protocol.TypeNotification,
		Body: protocol.FormatWorkerNotice(n, report), IdempotencyKey: protocol.NewID("k")}
	if parent != "" {
		p.Type, p.ParentID = protocol.TypeReply, parent
	}
	return d.send(ctx, p)
}

// noticeFor maps why a worker ended to the status and note of its notice. An
// empty status means no notice (the owner is gone, or there was never a session).
func (d *Daemon) noticeFor(reason string) (status, note string) {
	set := d.cfg.Workers.filled()
	switch reason {
	case protocol.StopIdle:
		return protocol.StatusTimedOut, "no message to or from the worker for " + set.Idle.String()
	case protocol.StopMaxAge:
		return protocol.StatusTimedOut, "the worker ran for the longest time allowed (" + set.MaxAge.String() + ")"
	case protocol.StopExited:
		return protocol.StatusFailed, "the harness process ended by itself, with no hand-back"
	case protocol.StopRequested:
		return protocol.StatusStopped, "stopped on request, with no hand-back"
	case protocol.StopLost:
		return protocol.StatusFailed, "the worker was lost while the daemon was not running"
	}
	return "", ""
}

// ---- hand-back ----

// handback serves "worker_handback" from a worker's session: the final report
// goes to the owner with the facts, and the worker is marked done.
func (d *Daemon) handback(ctx context.Context, sessionID string, req protocol.WorkerHandbackRequest) (*protocol.WorkerHandbackResult, error) {
	w := d.workerBySession(sessionID)
	if w == nil {
		return nil, errors.New("only a worker started by agent-net can hand back a report")
	}
	return d.handbackTo(ctx, w, req, "", "")
}

// handbackTo is handback to another session than the owner (see sendNoticeTo).
func (d *Daemon) handbackTo(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, to, replyTo string) (*protocol.WorkerHandbackResult, error) {
	d.wmu.Lock()
	sessionID := w.sessionID
	d.wmu.Unlock()
	if req.Status == "" {
		req.Status = protocol.StatusDone
	}
	if !protocol.ValidHandbackStatus(req.Status) {
		return nil, fmt.Errorf("status must be done, blocked or failed, not %q", req.Status)
	}
	if strings.TrimSpace(req.Report) == "" {
		return nil, errors.New("the report is empty: say what you did, or what blocks you")
	}
	st := d.workerStats(w)
	n := d.notice(w, req.Status, "", st)
	raw, err := d.sendNoticeTo(ctx, w, n, req.Report, to, replyTo)
	if err != nil {
		return nil, err
	}
	d.wmu.Lock()
	w.doneStatus = req.Status
	d.saveWorkerLocked(w)
	d.wmu.Unlock()
	d.Logf("worker.handback worker=%s session=%s status=%s", w.id, sessionID, req.Status)
	out := &protocol.WorkerHandbackResult{Status: req.Status}
	var sent protocol.MessageSent
	if json.Unmarshal(raw, &sent) == nil {
		out.Sent, out.Queued = sent.Message, sent.Queued
	}
	return out, nil
}

// noteAsk records a request to a worker from its owner. A hand-back answers
// the last one.
func (d *Daemon) noteAsk(m protocol.Message) {
	w := d.workerBySession(m.To)
	if w == nil || m.From != w.owner || m.Type != protocol.TypeRequest {
		return
	}
	d.wmu.Lock()
	w.taskMsg = m.ID
	d.saveWorkerLocked(w)
	d.wmu.Unlock()
}

// ---- a worker that was lost while the daemon was down ----

// lostNotice sends the notice of a worker found dead at start, from its row.
func (d *Daemon) lostNotice(ctx context.Context, r workerRow) {
	if r.DoneStatus != "" || r.SessionID == "" {
		return
	}
	w := &worker{id: r.ID, harness: r.Harness, project: r.Project, path: r.Path, model: r.Model, owner: r.Owner,
		sessionID: r.SessionID, startedAt: r.StartedAt, taskMsg: r.TaskMsg, mainPath: r.MainPath, worktree: r.Worktree,
		branch: r.Branch, base: r.Base}
	status, note := d.noticeFor(protocol.StopLost)
	if _, err := d.sendNotice(ctx, w, d.notice(w, status, note, workerStats{}), ""); err != nil {
		d.Logf("worker %s: notice: %v", r.ID, err)
	}
	d.finishWorktree(w)
}
