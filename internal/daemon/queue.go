package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The queue (decision 0018). A start that finds no room waits here, first come
// first served per machine. The daemon looks at the queue every
// protocol.WorkerQueueTick and starts the head when it fits. The queue is in
// daemon.db, so a restart keeps it.

// admitOrQueue lets a start through, or queues it. It returns nil when the
// start may go on now (the start time is reserved), else the answer to give.
func (d *Daemon) admitOrQueue(l protocol.WorkerLaunch) *protocol.WorkerLaunched {
	d.qmu.Lock()
	defer d.qmu.Unlock()
	ahead, err := d.st.queueLen()
	if err != nil {
		ahead = 0
	}
	dec := d.admission(l.Harness, l.Model)
	if ahead == 0 && dec.OK {
		d.lastStart = time.Now()
		d.reserveWorkerBaseLocked(l.Base)
		return nil
	}
	// A short background phrasing task must not outlive its caller in the queue.
	if l.RequireReadOnly {
		return &protocol.WorkerLaunched{Reason: "no room for a read-only background worker"}
	}
	q := queuedStart{ID: queueIDFor(l), Owner: l.Owner, OwnerName: l.OwnerName, Project: l.Project, Path: l.Path, Harness: l.Harness,
		Model: l.Model, Task: l.Task, Title: l.Title, Isolation: l.Isolation, Base: l.Base, RecordTask: l.RecordTask, Permissions: l.Permissions,
		DifficultyAsked: l.DifficultyAsked, DifficultyUsed: l.DifficultyUsed, Touches: strings.Join(l.Touches, ","), FromCTO: l.FromCTO, QueuedAt: time.Now(),
		Assessment: l.Assessment, AutoContinue: protocol.AutoContinueEnabled(l.AutoContinue), TaskID: l.TaskID, ContinuationPartial: l.ContinuationPartial, ChangeID: l.ChangeID}
	if err := d.st.addQueued(q); err != nil {
		// Without a queue the old behaviour is the safe one: refuse.
		return &protocol.WorkerLaunched{Reason: "cannot queue: " + err.Error()}
	}
	why := dec.Reason
	if dec.OK {
		why = fmt.Sprintf("%d start(s) are waiting ahead of it", ahead)
	}
	reason := fmt.Sprintf("queued #%d on %s: %s", ahead+1, d.hostName(), why)
	d.Logf("worker.queued queue=%s position=%d project=%s harness=%s owner=%s room=%s reason=%q",
		q.ID, ahead+1, l.Project, l.Harness, l.Owner, dec.Room, why)
	return &protocol.WorkerLaunched{Queued: true, QueueID: q.ID, Position: ahead + 1, Reason: reason}
}

// queueIDFor is the queue ID of a start: the reference the gateway tracks it
// under, when it gave one, so a report of the start names what the gateway has.
func queueIDFor(l protocol.WorkerLaunch) string {
	if protocol.IsQueueID(l.StartRef) {
		return l.StartRef
	}
	return protocol.NewID("q")
}

// queueLoop checks the queue on a timer.
func (d *Daemon) queueLoop(ctx context.Context) {
	t := time.NewTicker(d.workerSettings("").QueueTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.processQueue(ctx)
		}
	}
}

// processQueue ends the starts that waited too long or lost their owner, then
// starts the head of the queue if it fits.
func (d *Daemon) processQueue(ctx context.Context) {
	snap := d.SettingsSnapshot()
	entries, err := d.st.queue()
	if err != nil || len(entries) == 0 {
		return
	}
	now := time.Now()
	var live []queuedStart
	for _, e := range entries {
		set := d.workerSettingsWith(snap, e.Project)
		if now.Sub(e.QueuedAt) >= set.QueueTimeout {
			d.qmu.Lock()
			dec := d.admission(e.Harness, e.Model)
			gone := !d.st.removeQueued(e.ID)
			d.qmu.Unlock()
			if gone {
				continue
			}
			why := fmt.Sprintf("it waited %s in the queue and the machine still had no room", set.QueueTimeout)
			if dec.Reason != "" {
				why += " (" + dec.Reason + ")"
			}
			d.Logf("worker.not_started queue=%s project=%s harness=%s owner=%s reason=%q", e.ID, e.Project, e.Harness, e.Owner, why)
			d.notStarted(ctx, e, why)
			continue
		}
		live = append(live, e)
	}
	if !d.Connected() {
		return
	}
	// The caller left: drop its starts, as a running worker ends when its
	// owner is gone.
	kept := live[:0]
	checked := map[string]bool{} // owner -> offline
	for _, e := range live {
		set := d.workerSettingsWith(snap, e.Project)
		off, seen := checked[e.Owner]
		if !seen {
			off = d.ownerOffline(ctx, e.Owner)
			checked[e.Owner] = off
		}
		d.qmu.Lock()
		if !off {
			delete(d.qGone, e.ID)
		} else if d.qGone[e.ID].IsZero() {
			d.qGone[e.ID] = now
		}
		leave := !d.qGone[e.ID].IsZero() && now.Sub(d.qGone[e.ID]) >= set.OwnerGrace
		if leave && d.st.removeQueued(e.ID) {
			delete(d.qGone, e.ID)
			d.Logf("worker.queue_dropped queue=%s project=%s owner=%s reason=%q", e.ID, e.Project, e.Owner, "its owner ended or stayed offline")
		}
		d.qmu.Unlock()
		if !leave {
			kept = append(kept, e)
		}
	}
	if len(kept) == 0 {
		return
	}
	head := kept[0]
	d.qmu.Lock()
	if dec := d.admission(head.Harness, head.Model); !dec.OK || !d.st.removeQueued(head.ID) {
		d.qmu.Unlock()
		return
	}
	d.lastStart = time.Now()
	d.reserveWorkerBaseLocked(head.Base)
	delete(d.qGone, head.ID)
	d.qmu.Unlock()
	d.startQueued(ctx, head)
}

// startQueued starts the worker for a queue entry that has room. The owner's
// machine sends the task; this machine tells the owner it started.
func (d *Daemon) startQueued(ctx context.Context, e queuedStart) {
	l := protocol.WorkerLaunch{Project: e.Project, Path: e.Path, Harness: e.Harness, Model: e.Model, Owner: e.Owner, OwnerName: e.OwnerName,
		Task: e.Task, Title: e.Title, Isolation: e.Isolation, Base: e.Base, RecordTask: e.RecordTask, Permissions: e.Permissions,
		DifficultyAsked: e.DifficultyAsked, DifficultyUsed: e.DifficultyUsed, Touches: splitTouches(e.Touches), Assessment: e.Assessment,
		AutoContinue: &e.AutoContinue, TaskID: e.TaskID, ContinuationPartial: e.ContinuationPartial, ChangeID: e.ChangeID}
	out, err := d.runWorkerStart(ctx, l)
	if err != nil {
		d.clearStartReservation()
		d.Logf("worker.not_started queue=%s project=%s harness=%s owner=%s reason=%q", e.ID, e.Project, e.Harness, e.Owner, err.Error())
		d.notStarted(ctx, e, "the machine had room, but the start failed: "+err.Error())
		return
	}
	waited := time.Since(e.QueuedAt)
	d.Logf("worker.dequeued queue=%s session=%s project=%s harness=%s owner=%s waited=%s", e.ID, out.SessionID, e.Project, e.Harness, e.Owner, waited.Round(time.Second))
	// Sending the task may wait for a slow gateway for a long time. It runs on
	// its own, so the queue goes on: the worker is already counted as running,
	// which is what admission looks at.
	d.wmu.Lock()
	w := d.workers[out.WorkerID]
	d.wmu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.finishQueuedStart(ctx, e, out, w, waited)
	}()
}

// finishQueuedStart sends the task of a dequeued start and tells the owner it
// started. When the task cannot be sent, the worker is stopped and the owner is
// told; when the worker ended by itself first, its own end tells the owner.
func (d *Daemon) finishQueuedStart(ctx context.Context, e queuedStart, out protocol.WorkerLaunched, w *worker, waited time.Duration) {
	err := d.sendQueuedStart(ctx, w, d.workerSettings(e.Project).BackgroundStartTimeout, e.Project, protocol.WorkerQueuedStart{Ref: e.ID, Owner: e.Owner, SessionID: out.SessionID,
		Harness: e.Harness, Project: e.Project, Task: e.Task, Title: e.Title, Worktree: out.Worktree, Branch: out.Branch,
		ProjectRules: out.ProjectRules, Permissions: e.Permissions, OSSandbox: out.OSSandbox, FromCTO: e.FromCTO})
	if errors.Is(err, errWorkerEnded) {
		return
	}
	if err != nil {
		if w != nil {
			d.endWorker(w, protocol.StopStartFails)
		}
		d.notStarted(ctx, e, "the worker started, but its task could not be sent, so it was stopped: "+err.Error())
		return
	}
	if w != nil {
		body := protocol.WorkerStartedText(out.SessionID, e.Harness, e.Model, e.Project, d.hostName(), waited, e.Title) + protocol.WorkerPermissionsLine(e.Permissions, e.Harness, out.OSSandbox) + "\n"
		if _, err := d.send(ctx, protocol.MsgSend{Sender: out.SessionID, Recipient: e.Owner, Type: protocol.TypeNotification, Body: body}); err != nil {
			d.Logf("worker %s: started notice: %v", w.id, err)
		}
	}
}

// errWorkerEnded is a task send that stopped because the worker ended.
var errWorkerEnded = errors.New("the worker ended before its task was sent")

// sendQueuedStart asks the gateway to send a started worker its task
// ("worker.queued_start"). A gateway that is busy or restarting for an upgrade
// answers late or not at all, and that is no reason to stop a worker that is
// running. So the call is tried again while the link is up or reconnecting, or
// the gateway says the fault is transient, until the bound is over. The retry
// stops with the worker: a call that waits is cancelled when the worker ends. A
// gateway that answers with a refusal ends it at once. The task message has a
// fixed key (sendQueuedTask) and the gateway sends the task once, so a first
// call that was only slow does not send the task twice.
func (d *Daemon) sendQueuedStart(ctx context.Context, w *worker, within time.Duration, project string, p protocol.WorkerQueuedStart) error {
	if within <= 0 {
		within = d.workerSettings(project).BackgroundStartTimeout
	}
	bound, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	if w != nil {
		ended := d.workerEnded(w)
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			select {
			case <-ended:
				cancel()
			case <-stop:
			}
		}()
	}
	workerGone := func() bool {
		if w == nil {
			return false
		}
		select {
		case <-d.workerEnded(w):
			return true
		default:
			return false
		}
	}
	for attempt := 1; ; attempt++ {
		if workerGone() {
			return errWorkerEnded
		}
		// The gateway may take up to 30 s to relay the task to the owner's
		// machine, so a call waits a little longer than that.
		cctx, cancelCall := context.WithTimeout(withCallWait(bound, 35*time.Second), 30*time.Second)
		_, err := d.call(cctx, "worker.queued_start", p)
		cancelCall()
		if err == nil {
			return nil
		}
		if workerGone() {
			return errWorkerEnded
		}
		if bound.Err() != nil || !retryableGatewayError(err) {
			return err
		}
		d.Logf("worker.task_retry session=%s attempt=%d reason=%q", p.SessionID, attempt, err.Error())
		select {
		case <-bound.Done():
			if workerGone() {
				return errWorkerEnded
			}
			return err
		case <-time.After(d.cfg.BackoffMin):
		}
	}
}

// retryableGatewayError tells whether a failed gateway call may work later: the
// gateway did not answer, the link is down or coming back, or the gateway says
// the fault is transient. A refusal from the gateway is final.
func retryableGatewayError(err error) bool {
	return errors.Is(err, errGatewayTimeout) || errors.Is(err, errNotConnected) || errors.Is(err, errGatewayTransient) || errors.Is(err, context.DeadlineExceeded)
}

// notStarted tells the gateway that a queued start will not become a worker.
// The gateway records an event for the TUI feed and sends the owner a notice
// from the reserved agent-net sender. This machine never sends it: a notice
// sent as this machine's owner session would carry the owner's label and
// authority.
func (d *Daemon) notStarted(ctx context.Context, e queuedStart, why string) {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := d.call(rctx, "worker.not_started", protocol.WorkerNotStarted{Ref: e.ID, Owner: e.Owner, Project: e.Project, Harness: e.Harness, Reason: why, Permissions: e.Permissions}); err != nil {
		d.Logf("queue %s: not-started notice: %v", e.ID, err)
	}
}

// sendQueuedTask handles "worker.task": a queued start has become a worker on
// another machine, and this machine holds the owner, so it sends the task as
// the owner would have.
func (d *Daemon) sendQueuedTask(ctx context.Context, p protocol.WorkerQueuedStart) error {
	if p.Owner == "" || p.SessionID == "" || d.sessionByID(p.Owner) == nil {
		return errors.New("the owner of this queued start is not a session of this machine")
	}
	charter := d.charterBrief(ctx, p.Project)
	// The key is fixed per worker, so the same task sent twice is stored once.
	_, err := d.send(ctx, protocol.MsgSend{Sender: p.Owner, Recipient: p.SessionID, Type: protocol.TypeRequest, IdempotencyKey: "queued-task:" + p.SessionID,
		Body: protocol.WorkerTaskBodyOS(p.Harness, p.Task, p.Worktree, p.Branch, p.ProjectRules, charter, p.Permissions, p.OSSandbox, d.SettingsSnapshot(), p.Project)})
	return err
}

// cancelQueued removes a queued start. Only its owner or the person at a
// command line may.
func (d *Daemon) cancelQueued(id, requester string, asOwner bool, validate ...bool) error {
	entries, err := d.st.queue()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.ID != id {
			continue
		}
		if !asOwner && requester != e.Owner {
			return errors.New("only the session that queued the start, or the person at the command line, can cancel it")
		}
		if len(validate) > 0 && validate[0] {
			return nil
		}
		if !d.st.removeQueued(id) {
			break
		}
		d.Logf("worker.queue_cancelled queue=%s project=%s owner=%s by=%s", id, e.Project, e.Owner, requester)
		return nil
	}
	return fmt.Errorf("no worker with session %s runs on this machine (no queued start has that ID)", id)
}

// reserveWorkerBaseLocked keeps a branch used by an admitted start until its
// worktree checkout has finished. The caller holds qmu.
func (d *Daemon) reserveWorkerBaseLocked(base string) {
	if base == "" {
		return
	}
	if d.startingBases == nil {
		d.startingBases = make(map[string]int)
	}
	d.startingBases[base]++
}

func (d *Daemon) reserveWorkerBase(base string) {
	d.qmu.Lock()
	defer d.qmu.Unlock()
	d.reserveWorkerBaseLocked(base)
}

func (d *Daemon) releaseWorkerBase(base string) {
	if base == "" {
		return
	}
	d.qmu.Lock()
	defer d.qmu.Unlock()
	if d.startingBases[base] <= 1 {
		delete(d.startingBases, base)
	} else {
		d.startingBases[base]--
	}
}
