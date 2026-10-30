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
func (d *Daemon) createWorktree(ctx context.Context, w *worker, requestedBase ...string) error {
	main := w.path
	ref := "HEAD"
	if len(requestedBase) > 0 && requestedBase[0] != "" {
		name := requestedBase[0]
		if strings.HasPrefix(name, "-") {
			return fmt.Errorf("invalid base branch %q", name)
		}
		if _, err := gitOut(ctx, main, "check-ref-format", "--branch", name); err != nil {
			return fmt.Errorf("invalid base branch %q: %w", name, err)
		}
		ref = "refs/heads/" + name
	}
	base, err := gitOut(ctx, main, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		if ref != "HEAD" {
			return fmt.Errorf("base branch %q does not exist: %w", requestedBase[0], err)
		}
		return fmt.Errorf("cannot make a worktree: %s is not a git repository with a commit (%v)", main, err)
	}
	dir := filepath.Join(d.cfg.StateDir, "worktrees", w.id)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	branch := protocol.WorktreeBranch(w.id)
	if _, err := gitOut(ctx, main, "worktree", "add", dir, "-b", branch, base); err != nil {
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

// unfinishedSubject is the subject of the commit that keeps unfinished work.
func unfinishedSubject(workerID, reason string) string {
	return "agent-net: unfinished work of worker " + workerID + " (" + reason + ")"
}

// boundTask cuts a task to the 64 KiB that a worker record keeps.
func boundTask(s string) string {
	const max = 64 * 1024
	if len(s) > max {
		s = strings.ToValidUTF8(s[:max], "")
	}
	return s
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// saveUnfinished commits the uncommitted changes of a worker that ended with no
// hand-back, on the worker's branch, so that finishWorktree keeps them in a
// commit and a new worker can continue from them. It uses the repository's
// author when there is one, and "agent-net" when not. It tells whether a
// commit was made.
func (d *Daemon) saveUnfinished(w *worker, reason string) bool {
	d.wmu.Lock()
	dir, id := w.worktree, w.id
	d.wmu.Unlock()
	if dir == "" {
		return false
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return false
	}
	ctx := context.Background()
	if st, err := gitOut(ctx, dir, "status", "--porcelain"); err != nil || st == "" {
		return false
	}
	// A commit now would drop the merge parents (or the rest of a rebase), so
	// the worktree stays as it is. It is kept because it has changes.
	if gitOperationInProgress(ctx, dir) {
		d.wmu.Lock()
		w.leftAsIs = true
		d.wmu.Unlock()
		d.Logf("worker.unfinished_left worker=%s reason=%s path=%s (a git merge, rebase, cherry-pick or revert is in progress)", id, reason, dir)
		return false
	}
	if _, err := gitOut(ctx, dir, "add", "-A"); err != nil {
		d.Logf("worker %s: keep unfinished work: %v", id, err)
		return false
	}
	args := []string{"-c", "commit.gpgsign=false"}
	if v, _ := gitOut(ctx, dir, "config", "user.name"); v == "" {
		args = append(args, "-c", "user.name=agent-net")
	}
	if v, _ := gitOut(ctx, dir, "config", "user.email"); v == "" {
		args = append(args, "-c", "user.email=agent-net@localhost")
	}
	args = append(args, "commit", "-q", "--no-verify", "-m", unfinishedSubject(id, reason))
	if _, err := gitOut(ctx, dir, args...); err != nil {
		d.Logf("worker %s: keep unfinished work: %v", id, err)
		return false
	}
	d.wmu.Lock()
	w.unfinished = true
	d.wmu.Unlock()
	d.Logf("worker.unfinished_saved worker=%s reason=%s path=%s", id, reason, dir)
	return true
}

// gitOperationInProgress tells whether the worktree is in the middle of a
// merge, rebase, cherry-pick or revert.
func gitOperationInProgress(ctx context.Context, dir string) bool {
	for _, name := range []string{"MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		p, err := gitOut(ctx, dir, "rev-parse", "--git-path", name)
		if err != nil || p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
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
	// ContextTokens is the tokens the model saw on its last step, ContextLimit
	// the model's context window, both 0 when the harness cannot say.
	ContextTokens int64
	ContextLimit  int64
	// TotalTokens is input plus output plus reasoning for the run, no cache
	// reads. 0 when the harness cannot say.
	TotalTokens int64
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
	sid, harness, model, project, wt, br, started, permissions := w.sessionID, w.harness, w.model, w.project, w.worktree, w.branch, w.startedAt, w.permissions
	d.wmu.Unlock()
	if model == "" {
		model = st.Model
	}
	n := protocol.WorkerNotice{Status: status, Note: note, Session: sid, Harness: harness, Model: model, Project: project, Permissions: permissions,
		Host: d.hostName(), RunTime: time.Since(started), Tokens: st.Tokens, Context: protocol.ContextText(st.ContextTokens, st.ContextLimit),
		Cost: st.Cost, Worktree: wt, Branch: br}
	n.DiffStat, _ = d.worktreeFacts(w)
	d.wmu.Lock()
	n.Saved = w.unfinished
	leftAsIs := w.leftAsIs
	d.wmu.Unlock()
	if leftAsIs {
		n.Note = strings.TrimSpace(n.Note + " A git merge was in progress; the worktree was left as is.")
	}
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
	case protocol.StopModelFails:
		return protocol.StatusFailed, "the model call failed" // endWorker adds the error text
	case protocol.StopRunFailed:
		return protocol.StatusFailed, "the worker run failed"
	case protocol.StopRequested:
		return protocol.StatusStopped, "stopped on request, with no hand-back"
	case protocol.StopLost:
		return "lost after daemon restart", "the worker was lost while the daemon was not running"
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
	return d.handbackToNote(ctx, w, req, to, replyTo, "", true)
}

func (d *Daemon) handbackToNote(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, to, replyTo, sandboxNote string, end bool) (*protocol.WorkerHandbackResult, error) {
	d.wmu.Lock()
	sessionID, stopping := w.sessionID, w.stopping
	d.wmu.Unlock()
	if stopping {
		return nil, errors.New("the worker is ending")
	}
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
	d.wmu.Lock()
	pending, cc := len(w.turnQueue) > 0, w.codex
	d.wmu.Unlock()
	if cc != nil {
		cc.mu.Lock()
		pending = pending || len(cc.queue) > 0
		cc.mu.Unlock()
	}
	note := "the worker ended after its hand-back; a message from you starts it again"
	if pending {
		note = "the worker will run its queued messages before ending"
	} else if _, canResume := d.launchers[w.harness].(resumer); !canResume {
		note = "the worker ended after its hand-back; this harness cannot be started again (" + cannotResumeReason(w.harness) + ")"
	}
	if sandboxNote != "" {
		note = sandboxNote + "; " + note
	}
	n := d.notice(w, req.Status, note, st)
	raw, err := d.sendNoticeTo(ctx, w, n, req.Report, to, replyTo)
	if err != nil {
		return nil, err
	}
	d.wmu.Lock()
	var next *protocol.Message
	if w.harness == "opencode" && len(w.turnQueue) > 0 {
		m := w.turnQueue[0]
		w.turnQueue = w.turnQueue[1:]
		w.turnHandbackIdle = true
		next = &m
	} else {
		w.doneStatus = req.Status
		w.donePartial = !req.IsComplete()
		d.saveWorkerLocked(w)
	}
	d.wmu.Unlock()
	d.Logf("worker.handback worker=%s session=%s status=%s complete=%t", w.id, sessionID, req.Status, req.IsComplete())
	// Like a Claude Code subagent, a worker ends when it hands back (W10). Its
	// harness session is kept, so the owner's next message starts it again.
	if next != nil {
		go d.deliverQueuedWorkerTurn(*next)
	} else if end {
		go d.endWorker(w, protocol.StopHandback)
	}
	out := &protocol.WorkerHandbackResult{Status: req.Status, Complete: req.IsComplete()}
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
	var updated *protocol.WorkerInfo
	// The task in a list is the title: the first line of the FIRST task. A
	// later request from the owner must not replace it. Only an older worker
	// with no title yet takes the line of its first message.
	if task := protocol.WorkerTaskSummary(m.Body); task != "" && w.taskText == "" {
		w.taskText = task
		if w.info != nil {
			// Replace, never change in place: a response on another goroutine
			// may still be writing the old value.
			cp := *w.info
			cp.Task = task
			w.info = &cp
			updated = w.info
		}
	}
	d.saveWorkerLocked(w)
	sessionID := w.sessionID
	d.wmu.Unlock()
	if updated != nil {
		if ls := d.setSessionWorker(sessionID, updated); ls != nil {
			// Tell the gateway now: the row name follows the task, and the
			// session may not report again soon.
			go d.pushSession(ls)
		}
	}
}

// setSessionWorker gives the worker's local session a new worker value and
// returns it, or nil. The value is never changed in place, so a reader may
// keep the old one.
func (d *Daemon) setSessionWorker(sessionID string, info *protocol.WorkerInfo) *localSession {
	if sessionID == "" {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, ls := range d.sessions {
		if ls.sessionID == sessionID && ls.info.Worker != nil {
			ls.info.Worker = info
			return ls
		}
	}
	return nil
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
	// The new daemon committed the unfinished changes when it found the worker
	// lost (adoptWorkers). The commit subject tells.
	if w.worktree != "" {
		if s, err := gitOut(ctx, w.worktree, "log", "-1", "--format=%s"); err == nil && s == unfinishedSubject(w.id, protocol.StopLost) {
			w.unfinished = true
		}
	}
	if _, err := d.sendNotice(ctx, w, d.notice(w, status, note, workerStats{}), ""); err != nil {
		d.Logf("worker %s: notice: %v", r.ID, err)
	}
	d.finishWorktree(w)
}

// worktreesDir is the folder where this daemon creates the git worktrees of
// its workers, as a cleaned absolute path.
func (d *Daemon) worktreesDir() string {
	root, err := filepath.Abs(filepath.Join(d.cfg.StateDir, "worktrees"))
	if err != nil {
		return ""
	}
	return root
}

// dropWorktreeSessions closes the plugin connections of live sessions that
// work inside the worktrees folder and are not a worker's own. They are ghosts
// of workers that a user's own service registered before the hello refused
// them. Closing the connection takes the session offline.
func (d *Daemon) dropWorktreeSessions() {
	var drop []*localConn
	d.mu.Lock()
	for _, ls := range d.sessions {
		if ls.conn != nil && !ls.owned && ls.info.Worker == nil && d.inWorktreesDir(ls.info.Cwd) {
			drop = append(drop, ls.conn)
		}
	}
	d.mu.Unlock()
	for _, c := range drop {
		c.close()
	}
}

// inWorktreesDir tells whether cwd is inside the folder where this daemon
// creates the git worktrees of its workers. It compares cleaned absolute paths
// and works for a folder that no longer exists.
func (d *Daemon) inWorktreesDir(cwd string) bool {
	root := d.worktreesDir()
	if cwd == "" || root == "" {
		return false
	}
	p, err := filepath.Abs(cwd)
	if err != nil {
		return false
	}
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// queueOpenCodeTurn keeps later prompts in the daemon until the current turn
// hands back or becomes idle. This keeps them out of a process being ended.
func (d *Daemon) queueOpenCodeTurn(w *worker, m protocol.Message) (queued, retry bool) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.workers[w.id] != w || w.stopping || w.doneStatus != "" {
		return false, true
	}
	if w.harness != "opencode" {
		return false, false
	}
	if !w.turnActive {
		w.turnActive = true
		return false, false
	}
	w.turnQueue = append(w.turnQueue, m)
	return true, false
}

func (d *Daemon) deliverQueuedWorkerTurn(m protocol.Message) {
	ls := d.sessionByID(m.To)
	if ls == nil {
		d.sendUpdate(context.Background(), m.ID, protocol.StateFailed, "worker session is gone")
		return
	}
	d.noteAsk(m)
	res := d.deliverTo(context.Background(), ls, m)
	if !res.Delivered {
		d.sendUpdate(context.Background(), m.ID, protocol.StateFailed, res.Detail)
	}
}

// An OpenCode turn that did not hand back can finish normally. Let the next
// queued prompt run, or mark the worker ready for the next message.
func (d *Daemon) openCodeTurnIdle(session string) {
	d.wmu.Lock()
	var next *protocol.Message
	for _, w := range d.workers {
		if w.sessionID != session || w.harness != "opencode" || w.stopping || w.doneStatus != "" {
			continue
		}
		if w.turnHandbackIdle {
			w.turnHandbackIdle = false
			break
		}
		if len(w.turnQueue) == 0 {
			w.turnActive = false
		} else {
			m := w.turnQueue[0]
			w.turnQueue = w.turnQueue[1:]
			next = &m
		}
		break
	}
	d.wmu.Unlock()
	if next != nil {
		go d.deliverQueuedWorkerTurn(*next)
	}
}
