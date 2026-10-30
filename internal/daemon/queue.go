package daemon

import (
	"context"
	"errors"
	"fmt"
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
		return nil
	}
	q := queuedStart{ID: protocol.NewID("q"), Owner: l.Owner, Project: l.Project, Path: l.Path, Harness: l.Harness,
		Model: l.Model, Task: l.Task, Isolation: l.Isolation, Base: l.Base, RecordTask: l.RecordTask, Permissions: l.Permissions, QueuedAt: time.Now()}
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

// queueLoop checks the queue on a timer.
func (d *Daemon) queueLoop(ctx context.Context) {
	t := time.NewTicker(d.cfg.Workers.filled().QueueTick)
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
	set := d.cfg.Workers.filled()
	entries, err := d.st.queue()
	if err != nil || len(entries) == 0 {
		return
	}
	now := time.Now()
	var live []queuedStart
	for _, e := range entries {
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
	delete(d.qGone, head.ID)
	d.qmu.Unlock()
	d.startQueued(ctx, head)
}

// startQueued starts the worker for a queue entry that has room. The owner's
// machine sends the task; this machine tells the owner it started.
func (d *Daemon) startQueued(ctx context.Context, e queuedStart) {
	l := protocol.WorkerLaunch{Project: e.Project, Path: e.Path, Harness: e.Harness, Model: e.Model, Owner: e.Owner,
		Task: e.Task, Isolation: e.Isolation, Base: e.Base, RecordTask: e.RecordTask, Permissions: e.Permissions}
	out, err := d.runWorkerStart(ctx, l)
	if err != nil {
		d.Logf("worker.not_started queue=%s project=%s harness=%s owner=%s reason=%q", e.ID, e.Project, e.Harness, e.Owner, err.Error())
		d.notStarted(ctx, e, "the machine had room, but the start failed: "+err.Error())
		return
	}
	waited := time.Since(e.QueuedAt)
	d.Logf("worker.dequeued queue=%s session=%s project=%s harness=%s owner=%s waited=%s", e.ID, out.SessionID, e.Project, e.Harness, e.Owner, waited.Round(time.Second))
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, err = d.call(cctx, "worker.queued_start", protocol.WorkerQueuedStart{Owner: e.Owner, SessionID: out.SessionID,
		Harness: e.Harness, Project: e.Project, Task: e.Task, Worktree: out.Worktree, Branch: out.Branch,
		ProjectRules: out.ProjectRules, Permissions: e.Permissions})
	cancel()
	if err != nil {
		if w := d.workerBySession(out.SessionID); w != nil {
			d.endWorker(w, protocol.StopStartFails)
		}
		d.notStarted(ctx, e, "the worker started, but its task could not be sent, so it was stopped: "+err.Error())
		return
	}
	if w := d.workerBySession(out.SessionID); w != nil {
		body := protocol.WorkerStartedText(out.SessionID, e.Harness, e.Model, e.Project, d.hostName(), waited) + protocol.WorkerPermissionsText(e.Permissions, e.Harness) + "\n"
		if _, err := d.send(ctx, protocol.MsgSend{Sender: out.SessionID, Recipient: e.Owner, Type: protocol.TypeNotification, Body: body}); err != nil {
			d.Logf("worker %s: started notice: %v", w.id, err)
		}
	}
}

// notStarted tells the owner that a queued start will not become a worker. It
// is sent from this machine's owner session. When the owner is that session
// (the person here started it), there is no one to send to: the log has it.
func (d *Daemon) notStarted(ctx context.Context, e queuedStart, why string) {
	// The gateway records it for the TUI feed, which also covers the case
	// below where there is no one to send a message to.
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	d.call(rctx, "worker.not_started", protocol.WorkerNotStarted{Owner: e.Owner, Project: e.Project, Harness: e.Harness, Reason: why})
	cancel()
	from := d.ownerSessionID()
	if from == "" || from == e.Owner {
		return
	}
	body := protocol.WorkerNotStartedText(e.Project, e.Harness, why) + protocol.WorkerPermissionsText(e.Permissions, e.Harness) + "\n"
	if _, err := d.send(ctx, protocol.MsgSend{Sender: from, Recipient: e.Owner, Type: protocol.TypeNotification, Body: body}); err != nil {
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
	_, err := d.send(ctx, protocol.MsgSend{Sender: p.Owner, Recipient: p.SessionID, Type: protocol.TypeRequest,
		Body: protocol.WorkerTaskBodyWithCharter(p.Harness, p.Task, p.Worktree, p.Branch, p.ProjectRules, charter, p.Permissions)})
	return err
}

// cancelQueued removes a queued start. Only its owner or the person at a
// command line may.
func (d *Daemon) cancelQueued(id, requester string, asOwner bool) error {
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
		if !d.st.removeQueued(id) {
			break
		}
		d.Logf("worker.queue_cancelled queue=%s project=%s owner=%s by=%s", id, e.Project, e.Owner, requester)
		return nil
	}
	return fmt.Errorf("no worker with session %s runs on this machine (no queued start has that ID)", id)
}
