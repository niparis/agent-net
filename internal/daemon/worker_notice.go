package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Worker hand-back and completion notices (decision 0002, W10, W11 and W13).

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

// notice builds the facts of a completion notice.
func (d *Daemon) notice(w *worker, status, note string, st workerStats) protocol.WorkerNotice {
	d.wmu.Lock()
	sid, harness, model, project, wt, br, started, permissions, title, osSandbox := w.sessionID, w.harness, w.model, w.project, w.worktree, w.branch, w.startedAt, w.permissions, w.title, w.osSandbox
	cont, fix := w.continueCount, w.fixCount
	auto := w.autoContinueCount
	asked, used := w.difficultyAsked, w.difficultyUsed
	struggle := w.runStruggle()
	d.wmu.Unlock()
	if model == "" {
		model = st.Model
	}
	difficulty := first(asked, used)
	if difficulty == "" {
		difficulty = "normal"
	}
	n := protocol.WorkerNotice{Status: status, Note: note, Session: sid, Harness: harness, Model: model, Project: project, Title: title, Permissions: permissions, OSSandbox: osSandbox,
		Host: d.hostName(), RunTime: time.Since(started), Tokens: st.Tokens, Context: protocol.ContextText(st.ContextTokens, st.ContextLimit),
		Cost: st.Cost, Worktree: wt, Branch: br, Continue: cont, Fix: fix, AutoContinue: auto, Difficulty: difficulty, Struggle: struggle}
	n.APICost = d.apiCostText(harness, model, st.Usage)
	d.wmu.Lock()
	n.Evidence = w.evidence
	d.wmu.Unlock()
	n.DiffStat, _ = d.worktreeFacts(w)
	n.Head = d.worktreeHead(w)
	d.wmu.Lock()
	n.Saved = w.unfinished
	leftAsIs := w.leftAsIs
	excluded := w.excludedLeftovers
	d.wmu.Unlock()
	if excluded != "" {
		n.Note += "\nnot committed (build output or large file): " + excluded
	}
	if leftAsIs {
		n.Note = strings.TrimSpace(n.Note + " A git merge was in progress; the worktree was left as is.")
	}
	return n
}

// rateOffer asks the gateway whether the caller should be asked to rate this
// worker. It is best effort: an old gateway, or one that is not reachable,
// leaves the notice as it is.
func (d *Daemon) rateOffer(ctx context.Context, w *worker) bool {
	d.wmu.Lock()
	sid := w.sessionID
	d.wmu.Unlock()
	if sid == "" {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := d.call(cctx, "rate.offer", protocol.RateOfferFrame{SessionID: sid})
	if err != nil {
		return false
	}
	var out protocol.RateOfferResult
	if json.Unmarshal(raw, &out) != nil {
		return false
	}
	return out.Ask
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
	return d.sendNoticeToKey(ctx, w, n, report, to, replyTo, "", true)
}

func (d *Daemon) sendNoticeToKey(ctx context.Context, w *worker, n protocol.WorkerNotice, report, to, replyTo, noticeKey string, final bool) (json.RawMessage, error) {
	return d.sendNoticeToKeyWithFiles(ctx, w, n, report, to, replyTo, noticeKey, final, nil)
}

func (d *Daemon) sendNoticeToKeyWithFiles(ctx context.Context, w *worker, n protocol.WorkerNotice, report, to, replyTo, noticeKey string, final bool, files *OutgoingFiles) (json.RawMessage, error) {
	if final && d.rateOffer(ctx, w) {
		n.RateAsk = true
	}
	report = trimWorkerReport(report)
	body := d.workerToolchainPermissions(w, protocol.FormatWorkerNoticeWithSettings(n, report, d.SettingsSnapshot())) + renamedFilesNote(files)
	if strings.Contains(n.DeliveryError, handbackUncommittedPushProblem) {
		body = strings.Replace(body, "\nstatus: "+n.Status+"\n", "\nstatus: "+n.Status+", NOT PUSHED: uncommitted changes in the worktree\n", 1)
	}
	raw, err := d.sendWorkerNoticeBodyWithFiles(ctx, w, body, to, replyTo, noticeKey, files, n.Status == "idle without hand-back")
	if err == nil {
		switch n.Status {
		case protocol.StatusDone, protocol.StatusFailed, protocol.StatusTimedOut, "partial", "idle without hand-back", handbackRefusedStatus:
			d.wmu.Lock()
			if to == "" || to == w.owner {
				w.noticeSinceRequest = true
				d.saveWorkerLocked(w)
			}
			d.wmu.Unlock()
		}
	}
	return raw, err
}

// sendWorkerNoticeBody sends a worker notice through the same reply and outbox
// path used for completion notices.
func (d *Daemon) sendWorkerNoticeBody(ctx context.Context, w *worker, body, to, replyTo, noticeKey string) (json.RawMessage, error) {
	return d.sendWorkerNoticeBodyWithFiles(ctx, w, body, to, replyTo, noticeKey, nil, false)
}

func (d *Daemon) sendWorkerNoticeBodyWithFiles(ctx context.Context, w *worker, body, to, replyTo, noticeKey string, files *OutgoingFiles, idleNotice bool) (json.RawMessage, error) {
	d.wmu.Lock()
	sid, owner, parent := w.sessionID, w.owner, w.taskMsg
	d.wmu.Unlock()
	if to != "" {
		owner, parent = to, replyTo
	}
	if sid == "" {
		return nil, errors.New("the worker has no session")
	}
	if noticeKey == "" {
		noticeKey = protocol.NewID("k")
	}
	p := protocol.MsgSend{Sender: sid, Recipient: owner, Type: protocol.TypeNotification,
		Body: body, IdempotencyKey: noticeKey, WorkerIdleNotice: idleNotice, WorkerNotice: true}
	if parent != "" {
		p.Type, p.ParentID = protocol.TypeReply, parent
	}
	if files != nil {
		return d.sendWorkerNoticeFiles(ctx, FileSend{From: sid, To: owner, Body: body, Type: p.Type, ParentID: p.ParentID, IdempotencyKey: noticeKey, WorkerIdleNotice: idleNotice, WorkerNotice: true}, files)
	}
	return d.send(ctx, p)
}

// A stored notice must not be abandoned when the route lookup or a transfer
// fails. Reuse its key and checked snapshots; the receiver has its own bound
// and will deliver the report even if these attempts cannot finish.
func (d *Daemon) sendWorkerNoticeFiles(ctx context.Context, req FileSend, files *OutgoingFiles) (json.RawMessage, error) {
	transferCtx, cancel := context.WithTimeout(ctx, workerNoticeFileWait)
	defer cancel()
	var stored protocol.Message
	delay := 100 * time.Millisecond
	for {
		sent, err := d.sendPreparedFiles(transferCtx, req, files)
		if sent.Message.ID != "" {
			stored = sent.Message
		}
		if err == nil {
			return json.Marshal(protocol.MessageSent{Message: &sent.Message})
		}
		if stored.ID == "" {
			return nil, err
		}
		d.Logf("worker notice files: message=%s transfer failed, retrying: %v", stored.ID, err)
		timer := time.NewTimer(delay)
		select {
		case <-transferCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			d.Logf("worker notice files: message=%s transfer did not finish: %v", stored.ID, err)
			return json.Marshal(protocol.MessageSent{Message: &stored})
		case <-timer.C:
		}
		delay = min(delay*2, 5*time.Second)
	}
}

// prepareHandbackFiles limits workers to their own worktree, even when the
// project or /tmp would normally be a permitted source for a session.
func (d *Daemon) prepareHandbackFiles(w *worker, paths []string) (*OutgoingFiles, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	d.wmu.Lock()
	sid, root := w.sessionID, w.worktree
	d.wmu.Unlock()
	if root == "" {
		return nil, errors.New("a worker needs its own worktree to send files")
	}
	rules, err := d.fileRules(sid, false)
	if err != nil {
		return nil, err
	}
	rules.Roots, rules.Temp, rules.ConfineSymlinks = []string{root}, []string{}, true
	return PrepareFiles(paths, rules, filepath.Join(d.cfg.StateDir, fileOutgoingFolder))
}

// checkHandback sets the default status, checks the status and the report, and
// prepares the files once. The agy tool call runs it before it acknowledges the
// hand-back, so a refusal reaches the worker while it can hand back again in
// the same turn.
func (d *Daemon) checkHandback(w *worker, req *protocol.WorkerHandbackRequest, files *OutgoingFiles) (*OutgoingFiles, error) {
	if req.Status == "" {
		req.Status = protocol.StatusDone
	}
	if !protocol.ValidHandbackStatus(req.Status) {
		return nil, fmt.Errorf("status must be done, blocked or failed, not %q", req.Status)
	}
	if strings.TrimSpace(req.Report) == "" {
		return nil, errors.New("the report is empty: say what you did, or what blocks you")
	}
	if files != nil {
		return files, nil
	}
	files, err := d.prepareHandbackFiles(w, handbackPaths(*req))
	if err != nil {
		// The worker can fix the path or hand back without the file.
		return nil, err
	}
	return files, nil
}

// noticeFor maps why a worker ended to the status and note of its notice. An
// empty status means no notice (the owner is gone, or there was never a session).
func (d *Daemon) noticeFor(reason string) (status, note string) {
	set := d.workerSettings("")
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

// handback serves "worker_handback" from a worker's session: the final report
// goes to the owner with the facts, and the worker is marked done.
func (d *Daemon) handback(ctx context.Context, sessionID string, req protocol.WorkerHandbackRequest) (*protocol.WorkerHandbackResult, error) {
	w := d.workerBySession(sessionID)
	if w == nil {
		return nil, errors.New("only a worker started by agent-net can hand back a report")
	}
	if w.harness == "agy" {
		files, err := d.checkHandback(w, &req, nil)
		if err != nil {
			return nil, err
		}
		out, err := d.deferAgyHandback(w, req, files)
		if err != nil {
			files.Remove()
		}
		return out, err
	}
	return d.handbackFlow(ctx, w, req, "", "", "", true, nil)
}

func (d *Daemon) handbackToNote(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, to, replyTo, sandboxNote string, end bool) (*protocol.WorkerHandbackResult, error) {
	return d.handbackToNoteWithFiles(ctx, w, req, to, replyTo, sandboxNote, end, nil)
}

// An Antigravity handback carries its already checked snapshot until its
// result event. A recovered stream without one prepares it again.
func (d *Daemon) handbackToNoteWithFiles(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, to, replyTo, sandboxNote string, end bool, files *OutgoingFiles) (*protocol.WorkerHandbackResult, error) {
	return d.handbackFlow(ctx, w, req, to, replyTo, sandboxNote, end, files)
}

// handbackTail is what a hand-back carries from its record step to its notice.
type handbackTail struct {
	req                             protocol.WorkerHandbackRequest
	to, replyTo, sandboxNote        string
	end                             bool
	files                           *OutgoingFiles
	sessionID, committed, wipDetail string
	// committedFrom is the branch head before the daemon committed the
	// hand-back's leftovers, so a claimed head stays the change's head.
	committedFrom string
	complete      bool
	// frictionDropped is how many hand-back friction entries were dropped or
	// shortened before the notice.
	frictionDropped int
	// noticeKey is the idempotency key of the notice. An agy hand-back uses
	// the key its stream saved; another hand-back leaves it empty and the
	// sender makes one.
	noticeKey string
}

// handbackFlow records a hand-back and sends its notice.
func (d *Daemon) handbackFlow(ctx context.Context, w *worker, req protocol.WorkerHandbackRequest, to, replyTo, sandboxNote string, end bool, files *OutgoingFiles) (*protocol.WorkerHandbackResult, error) {
	d.wmu.Lock()
	sessionID, stopping := w.sessionID, w.stopping
	d.wmu.Unlock()
	if stopping {
		return nil, errors.New("the worker is ending")
	}
	// Friction is optional: clean a bad entry instead of refusing the whole
	// hand-back. The caller's notice says how many entries changed.
	var frictionDropped int
	req.Friction, frictionDropped = protocol.SanitizeFriction(req.Friction)
	handedOver := false
	files, err := d.checkHandback(w, &req, files)
	if err != nil {
		return nil, err
	}
	if files != nil {
		// The finish path takes over the snapshots and removes them.
		defer func() {
			if !handedOver {
				files.Remove()
			}
		}()
	}
	// A report that ends with "complete: no" is partial, whatever the harness
	// and even when the hand-back has no complete field (Codex reports are
	// read the same way). An explicit field wins.
	if req.Status == protocol.StatusDone && req.Complete == nil {
		if v, ok := protocol.WorkerReportComplete(req.Report); ok {
			req.Complete = &v
		}
	}
	// Carry the transfer manifest with each frame so names and hashes survive
	// the worker's worktree. The original path stays useful to a local reviewer.
	for i := range req.VisualProof {
		if files != nil {
			for j, f := range files.Files {
				if f.Source == req.VisualProof[i].File {
					files.Files[j].Entry.VisualProof = true
					entry := files.Files[j].Entry
					req.VisualProof[i].Manifest = &entry
					break
				}
			}
		}
	}
	// A partial hand-back is a worker that stopped early. Send it one more
	// message ourselves, up to the limit, and do not tell the caller yet.
	if req.Status == protocol.StatusDone && !req.IsComplete() && d.tryAutoContinue(ctx, w) {
		d.Logf("worker.handback worker=%s session=%s status=%s complete=false auto_continue=1", w.id, sessionID, req.Status)
		return &protocol.WorkerHandbackResult{Status: req.Status, Complete: false, AutoContinued: true}, nil
	}
	// Keep the explicit inability report for every ended run, including blocked
	// workers and workers without a delivery change. It survives daemon restarts.
	d.wmu.Lock()
	workerID, generation := w.id, w.runGeneration
	d.wmu.Unlock()
	if _, err := d.st.db.Exec(`INSERT INTO worker_visual_reports(worker_id,generation,reason) VALUES(?,?,?) ON CONFLICT(worker_id,generation) DO UPDATE SET reason=excluded.reason`, workerID, generation, req.ProofUnavailable); err != nil {
		d.Logf("worker visual report: %v", err)
	}
	complete := req.Status == protocol.StatusDone && req.IsComplete()
	// Read the run's own trace once, before the harness may be stopped, so
	// the notice and the delivery record carry the same counts.
	d.noteWorkerStruggle(ctx, w)
	d.captureWorkerEvidence(w, &complete)
	committedFrom := d.worktreeCommit(w)
	committed := d.commitAtHandback(w, req)
	wipDetail := ""
	if !complete && w.worktree != "" && w.harness != "agy" {
		unlock := d.lockWorktree(w.worktree)
		result := d.saveWorkerEndWIP(w, "handback")
		unlock()
		wipDetail = result.noticeDetail()
		if result.Failed != "" {
			d.Logf("worker %s: end WIP commit: %s", w.id, result.Failed)
		}
	}
	// A complete hand-back is a revision of a delivery change (delivery
	// pipeline build step 1). A partial one records nothing.
	t := &handbackTail{req: req, to: to, replyTo: replyTo, sandboxNote: sandboxNote, end: end, files: files,
		sessionID: sessionID, committed: committed, committedFrom: committedFrom, wipDetail: wipDetail, complete: complete, frictionDropped: frictionDropped}
	out, err := d.finishHandback(ctx, w, t)
	if errors.Is(err, errHandbackDeferred) && harnessKeepsHandback(w.harness) {
		out, err = d.finishWhenLinked(ctx, w, t)
	}
	// A hand-back held in the harness state keeps its snapshots for the next
	// connect.
	handedOver = errors.Is(err, errHandbackDeferred)
	return out, err
}

// finishWhenLinked finishes a hand-back that has no saved row
// (harnessKeepsHandback) once the gateway link is back. It finishes the same
// tail, so the notice still names what this hand-back committed, and the
// change gets the head the worktree has when it is sent. Until then the
// worker is not nudged, reported idle or ended. A daemon that stops first
// returns errHandbackDeferred with nothing saved: the harness state hands
// the turn back again at the next adoption.
func (d *Daemon) finishWhenLinked(ctx context.Context, w *worker, t *handbackTail) (*protocol.WorkerHandbackResult, error) {
	d.wmu.Lock()
	w.handbackPending = true
	d.wmu.Unlock()
	defer func() {
		d.wmu.Lock()
		w.handbackPending = false
		d.wmu.Unlock()
	}()
	stop := d.streamCtx()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if !d.waitForLink(stop) {
			return nil, errHandbackDeferred
		}
		out, err := d.finishHandback(ctx, w, t)
		if !errors.Is(err, errHandbackDeferred) {
			return out, err
		}
		d.Logf("worker %s: the hand-back waits for the gateway link again", w.id)
		select {
		case <-stop.Done():
			return nil, errHandbackDeferred
		case <-tick.C:
		}
	}
}

// finishHandback records the change and sends the notice.
func (d *Daemon) finishHandback(ctx context.Context, w *worker, t *handbackTail) (*protocol.WorkerHandbackResult, error) {
	req, to, replyTo, sandboxNote, end, files := t.req, t.to, t.replyTo, t.sandboxNote, t.end, t.files
	sessionID, committed, wipDetail := t.sessionID, t.committed, t.wipDetail
	changeID, deliveryProblem := "", ""
	var pick handbackHead
	if t.complete {
		rec := d.submitHandbackChangeFrom(ctx, w, nil, req.Report, t.committedFrom, req.VisualEvidence)
		changeID, deliveryProblem, pick = rec.changeID, rec.problem, rec.pick
		if rec.offline {
			if harnessKeepsHandback(w.harness) {
				// handbackFlow waits for the link and finishes this hand-back
				// again, so the change gets the head then.
				d.Logf("worker.handback_deferred worker=%s reason=%q", w.id, "the daemon has no gateway link")
				return nil, errHandbackDeferred
			}
			return nil, errors.New("agent-net is not connected to the gateway, so the change was not recorded; hand back again when the link is back")
		}
	}
	// Friction is best effort and never fails a hand-back.
	d.reportFriction(ctx, w, req.Friction, changeID)
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
	if w.worktree != "" {
		note += " while its worktree and branch exist"
	} else {
		note += fmt.Sprintf(" until %s after it started", d.workerSettings("").MaxAge)
	}
	if pending {
		note = "the worker will run its queued messages before ending"
	} else if _, canResume := d.launchers[w.harness].(resumer); !canResume {
		note = "the worker ended after its hand-back; this harness cannot be started again (" + cannotResumeReason(w.harness) + ")"
	}
	if sandboxNote != "" {
		note = sandboxNote + "; " + note
	}
	n := d.notice(w, req.Status, note, st)
	n.VisualEvidence = req.VisualEvidence
	n.Committed = committed
	n.WIPDetail = wipDetail
	n.FrictionDropped = t.frictionDropped
	n.Change, n.DeliveryError = changeID, deliveryProblem
	if changeID != "" {
		if pick.head == "" {
			n.DeliveryBranch = d.deliveredBranch(ctx, w)
		} else {
			if pick.branch != w.branch {
				n.DeliveryBranch = pick.branch
			}
			if n.Head == "" || !strings.HasPrefix(pick.head, n.Head) {
				n.DeliveryHead = shortHash(pick.head)
			}
			n.DeliveryClaim = pick.claimText()
		}
	}
	noticeKey := t.noticeKey
	if w.harness == "agy" {
		d.wmu.Lock()
		c := w.agy
		d.wmu.Unlock()
		if c != nil {
			c.mu.Lock()
			noticeKey = c.pendingKey
			c.mu.Unlock()
		}
	}
	// The notice goes out before the worker is ended, and its text says that
	// a message from the owner starts the worker again. Mark the hand-back
	// first, so a continue that arrives with the notice waits for the end
	// instead of being refused as "still running" (see continueWorker).
	d.wmu.Lock()
	w.handingBack = true
	d.wmu.Unlock()
	raw, err := d.sendNoticeToKeyWithFiles(ctx, w, n, req.Report, to, replyTo, noticeKey, true, files)
	if err != nil {
		d.wmu.Lock()
		w.handingBack = false
		d.wmu.Unlock()
		// A notice with files needs the link; one without goes to the outbox.
		return nil, err
	}
	d.wmu.Lock()
	var next *protocol.Message
	if (w.harness == "opencode" || w.harness == "agy") && len(w.turnQueue) > 0 {
		w.turnActive = true
		m := w.turnQueue[0]
		w.turnQueue = w.turnQueue[1:]
		w.turnHandbackIdle = w.harness == "opencode"
		w.turnAwaitingStart = true
		w.turnMessageID = m.ID
		next = &m
		w.handingBack = false
	} else {
		w.doneStatus = req.Status
		w.donePartial = !req.IsComplete()
		w.handbackReport = req.Report
		w.pendingHandback = true
		w.pendingPartial = !req.IsComplete()
		if !end {
			w.handingBack = false
		}
		d.saveWorkerLocked(w)
	}
	d.wmu.Unlock()
	d.Logf("worker.handback worker=%s session=%s status=%s complete=%t", w.id, sessionID, req.Status, req.IsComplete())
	// Like a Claude Code subagent, a worker ends when it hands back (W10). Its
	// harness session is kept, so the owner's next message starts it again.
	if next != nil {
		d.goBackground(func() { d.deliverQueuedWorkerTurn(*next) })
	} else if end {
		d.goBackground(func() { d.endWorker(w, protocol.StopHandback) })
	}
	out := &protocol.WorkerHandbackResult{Status: req.Status, Complete: req.IsComplete()}
	var sent protocol.MessageSent
	if json.Unmarshal(raw, &sent) == nil {
		out.Sent, out.Queued = sent.Message, sent.Queued
	}
	return out, nil
}

// commitAtHandback commits the worker's leftover changes at a hand-back, so
// the hand-back names a real commit (delivery pipeline, decision 8). It is the
// one path for every harness: a complete hand-back (status done, complete
// yes) is committed with the worker's title as the subject and the usual
// trailers. A partial one is handled by the ordered worker-end WIP commit.
// Files matched by .gitignore are not added, and nothing is forced. A merge or
// rebase in progress leaves the worktree as it is (saveWorkerChanges says so
// in the notice). It returns the short hash of the commit it made, or "".
func (d *Daemon) commitAtHandback(w *worker, req protocol.WorkerHandbackRequest) string {
	complete := req.Status == protocol.StatusDone && req.IsComplete()
	// The merge line is about this hand-back, not an earlier one.
	d.wmu.Lock()
	w.leftAsIs = false
	d.wmu.Unlock()
	if !complete && w.harness != "agy" {
		return ""
	}
	d.wmu.Lock()
	subject := strings.TrimSpace(strings.SplitN(w.title, "\n", 2)[0])
	d.wmu.Unlock()
	if subject == "" {
		subject = strings.TrimSpace(strings.SplitN(req.Report, "\n", 2)[0])
	}
	result := d.saveWorkerChanges(w, subject, "handback", !complete)
	if !complete {
		return ""
	}
	return result.SHA
}

// noteAsk records a message from a worker's owner and counts a follow-up after
// a hand-back. A hand-back answers the last request; messages from the owner
// after it are continues when it said complete:no, and fixes when it said
// complete:yes. A worker that went idle without a hand-back counts a continue
// too: its follow-up is the owner telling it to carry on. Only the owner
// counts: a notice, or another session, does not.
func (d *Daemon) noteAsk(m protocol.Message) {
	w := d.workerBySession(m.To)
	if w == nil || (m.From != w.owner && !(m.From == "agent-net" && protocol.IsWorkerTask(m))) {
		return
	}
	isAsk := m.Type == protocol.TypeRequest
	if !isAsk && m.Type != protocol.TypeReply {
		return
	}
	d.wmu.Lock()
	counted := false
	if w.pendingHandback || w.idleNoticeSent || w.idleFollowUp {
		// Every message from the owner after a hand-back counts until the
		// worker hands back again. An idle notice with no hand-back is the
		// same signal: the worker stopped early without saying so, so the
		// owner's follow-up is a continue. A notice or another session does
		// not count.
		cont, fix := protocol.ReworkCounts(w.pendingHandback, w.pendingPartial, w.idleNoticeSent || w.idleFollowUp)
		w.continueCount += cont
		w.continueRun += cont
		w.fixCount += fix
		w.fixRun += fix
		w.idleFollowUp = false
		counted = true
	}
	var updated *protocol.WorkerInfo
	if isAsk {
		w.taskMsg = m.ID
		// The task in a list is the title: the first line of the FIRST task. A
		// later request from the owner must not replace it. Only an older worker
		// with no title yet takes the line of its first message.
		if task := protocol.WorkerTaskSummary(m.Body); task != "" && w.taskText == "" {
			w.taskText = task
		}
	}
	if counted || (w.info != nil && isAsk) {
		if w.info == nil {
			w.info = &protocol.WorkerInfo{ID: w.id, Owner: w.owner, Project: w.project, StartedAt: w.startedAt.Unix(),
				Permissions: protocol.WorkerPermissionsOrDefault(w.permissions)}
		}
		// Replace, never change in place: a response on another goroutine may
		// still be writing the old value.
		cp := *w.info
		cp.Task = w.taskText
		cp.Continue, cp.Fix = w.continueCount, w.fixCount
		cp.AutoContinue = w.autoContinueCount
		cp.DifficultyAsked, cp.DifficultyUsed = w.difficultyAsked, w.difficultyUsed
		cp.Touches = splitTouches(w.touches)
		w.info = &cp
		// The session gets its own copy, so a later change to w.info never
		// touches a value that pushSession may be encoding.
		snap := cp
		updated = &snap
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

// noteMidrun counts on arrival, before an OpenCode prompt can wait in its
// turn queue. noteAsk still counts the existing rework when it is delivered.
func (d *Daemon) noteMidrun(w *worker, m protocol.Message) bool {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.workers[w.id] != w || w.stopping {
		return false
	}
	// Queued turns remain dispatched until the plugin confirms them. A
	// repeated gateway push must not count that same request twice, including
	// the gap between dequeueing it and claiming its harness write.
	if w.turnAwaitingStart && w.turnMessageID == m.ID {
		return true
	}
	for _, queued := range w.turnQueue {
		if queued.ID == m.ID {
			return true
		}
	}
	if m.Type == protocol.TypeRequest && m.From == w.owner {
		if w.midrunSeen && !w.noticeSinceRequest {
			w.midrunCount++
			w.midrunRun++
		}
		w.midrunSeen = true
		w.noticeSinceRequest = false
		d.saveWorkerLocked(w)
	}
	return true
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

// lostNotice sends the notice of a worker found dead at start, from its row.
func (d *Daemon) lostNotice(ctx context.Context, r workerRow) {
	if r.DoneStatus != "" || r.SessionID == "" {
		return
	}
	w := &worker{evidence: r.Evidence, id: r.ID, harness: r.Harness, project: r.Project, path: r.Path, model: r.Model, owner: r.Owner, ownerName: r.OwnerName,
		sessionID: r.SessionID, startedAt: r.StartedAt, taskMsg: r.TaskMsg, mainPath: r.MainPath, worktree: r.Worktree,
		branch: r.Branch, base: r.Base, title: r.Title}
	state, err := d.st.loadWorkerLostNotice(r.ID, r.RunGeneration)
	if err != nil {
		d.Logf("worker %s: lost notice state: %v", r.ID, err)
	}
	status, note := d.noticeFor(protocol.StopLost)
	d.noteWorkerStruggle(ctx, w)
	unlock := d.lockWorktree(w.worktree)
	result := d.saveWorkerEndWIP(w, protocol.StopLost)
	unlock()
	if result.Failed != "" {
		d.Logf("worker %s: end WIP commit: %s", r.ID, result.Failed)
	}
	if result.SHA == "" && w.worktree != "" {
		if subject, err := gitOut(ctx, w.worktree, "log", "-1", "--format=%s"); err == nil {
			switch subject {
			case unfinishedSubject(w.id, protocol.StopLost):
				w.unfinished = true
			case "WIP: uncommitted changes at worker end (lost)":
				sha, _ := gitOut(ctx, w.worktree, "rev-parse", "--short", "HEAD")
				result = workerEndCommitResult{SHA: sha, Files: workerCommitFileCount(ctx, w.worktree, "HEAD")}
			default:
				if w.harness == "agy" && strings.TrimSpace(subject) == strings.TrimSpace(w.title) {
					w.unfinished = true
				}
			}
		}
	}
	// One lost run tells its caller once, however often the gateway report is
	// retried. A WIP save that succeeds after the notice is a short follow-up.
	if !state.Sent {
		n := d.notice(w, status, note, workerStats{})
		n.WIPDetail = result.noticeDetail()
		if _, err := d.sendNotice(ctx, w, n, ""); err != nil {
			d.Logf("worker %s: notice: %v", r.ID, err)
			d.finishWorktree(w)
			return
		}
		state.Sent = true
		if result.SHA != "" {
			state.WIPSHA = result.SHA
		}
		if err := d.st.saveWorkerLostNotice(r.ID, r.RunGeneration, state); err != nil {
			d.Logf("worker %s: save lost notice state: %v", r.ID, err)
		}
		d.finishWorktree(w)
		return
	}
	if result.SHA != "" && !state.WIPSent && result.SHA != state.WIPSHA {
		if _, err := d.sendWorkerNoticeBody(ctx, w, "WIP saved as "+result.SHA, "", "", ""); err != nil {
			d.Logf("worker %s: WIP saved notice: %v", r.ID, err)
		} else {
			state.WIPSHA, state.WIPSent = result.SHA, true
			if err := d.st.saveWorkerLostNotice(r.ID, r.RunGeneration, state); err != nil {
				d.Logf("worker %s: save lost notice state: %v", r.ID, err)
			}
		}
	}
	d.finishWorktree(w)
}

// handbackPaths uses the same transfer and confinement rules for proof frames.
func handbackPaths(req protocol.WorkerHandbackRequest) []string {
	paths := append([]string(nil), req.Files...)
	for _, proof := range req.VisualProof {
		found := false
		for _, p := range paths {
			if p == proof.File {
				found = true
				break
			}
		}
		if !found {
			paths = append(paths, proof.File)
		}
	}
	return paths
}

// apiCostText is the run's cost at API prices for the notice, asked of the
// gateway, which keeps the price list (decision 0161). It is empty when the
// harness reported no usage, the gateway has no price list, or it cannot be
// reached in two seconds: the notice never waits longer for a cost.
func (d *Daemon) apiCostText(harness, model string, usage protocol.RunTokens) string {
	if usage.Zero() || model == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, err := d.call(ctx, "worker.cost", protocol.WorkerCostQuery{Model: protocol.PickerModelKey(harness, model), Tokens: usage})
	if err != nil {
		return ""
	}
	var r protocol.WorkerCostReply
	if json.Unmarshal(raw, &r) != nil || !r.Known {
		return ""
	}
	return protocol.APICostText(r.Cost, r.PriceKnown)
}
