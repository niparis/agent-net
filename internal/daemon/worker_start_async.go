package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// workerStartAnswerBudget is how long worker_start waits for the whole start
// before it answers "accepted" and lets the judge and the launch go on in the
// background. The harness MCP client of the caller gives up after 10 s (Pi),
// and agent-net cannot raise that for every harness.
const workerStartAnswerBudget = 5 * time.Second

type phasesKey struct{}

// answerEarlyKey marks a worker_start call from an agent, which may be
// answered "accepted" while the start goes on.
type answerEarlyKey struct{}

// callPhases records how long each part of one call took, for the slow log.
type callPhases struct {
	mu    sync.Mutex
	order []string
	ms    map[string]int64
	extra map[string]string
}

// phasesOf is the recorder of the call that ctx belongs to. A context without
// one gets a recorder that nothing reads.
func phasesOf(ctx context.Context) *callPhases {
	if ph, ok := ctx.Value(phasesKey{}).(*callPhases); ok {
		return ph
	}
	return &callPhases{}
}

// begin starts timing a part and returns the function that ends it. A part
// that is timed twice adds up.
func (p *callPhases) begin(name string) func() {
	started := time.Now()
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.ms == nil {
			p.ms = map[string]int64{}
		}
		if _, ok := p.ms[name]; !ok {
			p.order = append(p.order, name)
		}
		p.ms[name] += time.Since(started).Milliseconds()
	}
}

// note adds detail that another part reported, such as the gateway's own
// split of its time, to the named part.
func (p *callPhases) note(name, detail string) {
	if detail == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.extra == nil {
		p.extra = map[string]string{}
	}
	p.extra[name] = detail
}

// String is "validate=1 precheck=5 judge=30123 gateway=80(picker=12 launch=40)".
func (p *callPhases) String() string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var parts []string
	for _, name := range p.order {
		part := fmt.Sprintf("%s=%d", name, p.ms[name])
		if detail := p.extra[name]; detail != "" {
			part += "(" + detail + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

// precheckWorkerStart asks the gateway for the checks of a start that need no
// judge, so that a start that cannot run is refused at once, with the reason
// and the picker's alternative, and not after the judge. A gateway that does
// not know the frame is an older one: its checks then run in the start itself.
func (d *Daemon) precheckWorkerStart(ctx context.Context, frame protocol.WorkerStartFrame, cto bool) error {
	cctx := withCallWait(ctx, 10*time.Second)
	var err error
	if cto {
		_, err = d.ctoAttributedCall(cctx, "worker.precheck", frame)
	} else {
		_, err = d.call(cctx, "worker.precheck", frame)
	}
	if err == nil || strings.Contains(err.Error(), "unknown frame type") {
		return nil
	}
	return err
}

type startOutcome struct {
	res *protocol.WorkerStartResult
	err error
}

// startWithinBudget runs a start and answers within the budget. When the
// start is done in time the caller gets its normal answer. When it is not, the
// caller gets "accepted" with a start id, and the end of the start reaches the
// caller as a notice (started, queued or not started).
func (d *Daemon) startWithinBudget(ctx context.Context, caller string, p protocol.WorkerStartRequest, ph *callPhases, run func(context.Context) (*protocol.WorkerStartResult, error)) (*protocol.WorkerStartResult, error) {
	budget := workerStartAnswerBudget
	if d.cfg.testStartBudget > 0 {
		budget = d.cfg.testStartBudget
	}
	id := protocol.NewID("a")
	began := time.Now()
	var mu sync.Mutex
	detached := false
	done := make(chan startOutcome, 1)
	d.goBackground(func() {
		// The start outlives the request: the caller may already have its
		// answer, or have gone.
		res, err := run(d.ctoCtx)
		d.slowCall("worker_start.background", began, ph)
		mu.Lock()
		if !detached {
			done <- startOutcome{res, err}
			mu.Unlock()
			return
		}
		mu.Unlock()
		d.settleAccepted(id, res, err)
		d.reportAcceptedStart(id, caller, p, res, err)
	})
	timer := time.NewTimer(budget)
	defer timer.Stop()
	var ctxErr error
	select {
	case o := <-done:
		return o.res, o.err
	case <-timer.C:
	case <-ctx.Done():
		ctxErr = ctx.Err()
	}
	mu.Lock()
	select {
	case o := <-done:
		mu.Unlock()
		return o.res, o.err
	default:
	}
	// Recorded under mu, before the background start can see detached, so
	// that its outcome is never lost.
	detached = true
	d.noteAccepted(id)
	mu.Unlock()
	d.Logf("worker.accepted start=%s project=%s harness=%s model=%s after=%dms", id, p.Project, p.Harness, p.Model, time.Since(began).Milliseconds())
	if ctxErr != nil {
		return nil, ctxErr
	}
	return &protocol.WorkerStartResult{Status: "accepted", StartID: id, Project: p.Project, Harness: p.Harness, Model: p.Model,
		Note: "the start is accepted: the difficulty judge and the launch go on in the background. A worker notice arrives in your inbox when the worker starts, waits in the queue, or does not start. Do not start it again."}, nil
}

// reportAcceptedStart tells the caller how an accepted start ended.
func (d *Daemon) reportAcceptedStart(id, caller string, p protocol.WorkerStartRequest, res *protocol.WorkerStartResult, err error) {
	ctx, cancel := context.WithTimeout(d.ctoCtx, 30*time.Second)
	defer cancel()
	switch {
	case err != nil:
		d.Logf("worker.not_started start=%s project=%s harness=%s reason=%q", id, p.Project, p.Harness, err.Error())
		d.startReport(ctx, protocol.WorkerStartReport{ID: id, Owner: caller, Project: p.Project, Harness: p.Harness, Status: protocol.StartReportNotStarted, Note: err.Error()})
	case res.Status == "queued":
		note := fmt.Sprintf("the machine has no room now: the start waits in its queue (queue id %s, position %d)", res.QueueID, res.Position)
		if res.QueueReason != "" {
			note += ": " + res.QueueReason
		}
		note += ". A notice follows when it starts."
		d.startReport(ctx, protocol.WorkerStartReport{ID: id, Owner: caller, Project: res.Project, Harness: res.Harness, Status: protocol.StartReportQueued, Note: note})
	default:
		body := protocol.WorkerAcceptedStartText(id, res.Status, res.SessionID, res.Harness, res.Model, res.Project, res.Hostname, res.Worktree, res.Branch, res.OwnerLine, res.Why)
		if _, serr := d.send(ctx, protocol.MsgSend{Sender: res.SessionID, Recipient: caller, Type: protocol.TypeNotification, Body: body}); serr != nil {
			d.Logf("worker %s: accepted start notice: %v", res.SessionID, serr)
		}
	}
}

// startReport sends the gateway the end of an accepted start, which makes the
// notice to the caller. A gateway that does not know the frame cannot.
func (d *Daemon) startReport(ctx context.Context, r protocol.WorkerStartReport) {
	if _, err := d.call(ctx, "worker.start_report", r); err != nil {
		d.Logf("worker.start_report start=%s: %v", r.ID, err)
	}
}

// acceptedStart is what the daemon that took an accepted start knows of it, so
// that agent_worker_stop can act on the start id the caller was given. Settled
// is false while the judge or the launch runs. A settled start is queued (Queue
// names it), has a worker (Session), or did not start (neither).
type acceptedStart struct {
	Queue, Session string
	Settled        bool
}

// noteAccepted records an accepted start. It is called before the answer goes
// out, so a stop that follows the answer finds the start.
func (d *Daemon) noteAccepted(id string) {
	d.acceptedMu.Lock()
	defer d.acceptedMu.Unlock()
	if d.accepted == nil {
		d.accepted = map[string]*acceptedStart{}
	}
	d.accepted[id] = &acceptedStart{}
}

// settleAccepted records how an accepted start ended.
func (d *Daemon) settleAccepted(id string, res *protocol.WorkerStartResult, err error) {
	d.acceptedMu.Lock()
	defer d.acceptedMu.Unlock()
	e := d.accepted[id]
	if e == nil {
		return
	}
	e.Settled = true
	switch {
	case err != nil || res == nil:
		// Did not start: nothing is queued and no worker exists.
	case res.Status == "queued":
		e.Queue = res.QueueID
	default:
		e.Session = res.SessionID
	}
}

// queuedStartBegan moves the accepted start that waited under the queue id to
// the worker it became. The machine of the owner sends that task, so this
// daemon sees it.
func (d *Daemon) queuedStartBegan(queue, session string) {
	if queue == "" {
		return
	}
	d.acceptedMu.Lock()
	defer d.acceptedMu.Unlock()
	for _, e := range d.accepted {
		if e.Queue == queue {
			e.Queue, e.Session = "", session
		}
	}
}

// startTarget is the id that agent_worker_stop acts on for id. A start id the
// caller was given stands for its queued start, or for the worker it became.
func (d *Daemon) startTarget(id string) (string, error) {
	if !protocol.IsStartID(id) {
		return id, nil
	}
	d.acceptedMu.Lock()
	e, ok := d.accepted[id]
	var st acceptedStart
	if ok {
		st = *e
	}
	d.acceptedMu.Unlock()
	switch {
	case ok && !st.Settled:
		return "", fmt.Errorf("start %s is still being judged or launched: stop it again when its notice says it is queued or started", id)
	case ok && st.Session != "":
		return st.Session, nil
	case ok && st.Queue != "":
		return st.Queue, nil
	case ok:
		return "", fmt.Errorf("start %s did not start, so there is nothing to stop", id)
	}
	d.heldMu.Lock()
	_, held := d.heldStarts[id]
	d.heldMu.Unlock()
	if held {
		return "", fmt.Errorf("start %s waits for your owner's answer on the Desk, so it cannot be stopped from here", id)
	}
	return "", fmt.Errorf("%s is a start id this daemon does not track: it was given by an earlier run of this daemon, or it never existed. Use the queue id (q_...) from the queued notice, or the worker's session id", id)
}
