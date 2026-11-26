package daemon

import (
	"context"

	"github.com/niparis/agent-net/internal/protocol"
)

// Holding and delivering a harness turn that arrives while another runs.

// queueWorkerEnd keeps end notices in decision order without holding up the
// worker loop. The worker stays registered until its notice and cleanup finish,
// so a resume cannot change the facts that go into the notice.
func (d *Daemon) queueWorkerEnd(fn func()) <-chan struct{} {
	d.workerEndMu.Lock()
	previous := d.workerEndTail
	done := make(chan struct{})
	d.workerEndTail = done
	// Register before Stop closes admission and waits for background work. An
	// end already in progress when admission closes must still send (or reach
	// the durable outbox), so its producer finishes it inline during shutdown.
	d.bgMu.Lock()
	background := !d.bgClosed
	if background {
		d.bg.Add(1)
	}
	d.bgMu.Unlock()
	d.workerEndMu.Unlock()
	run := func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		fn()
	}
	if background {
		go func() {
			defer d.bg.Done()
			run()
		}()
	} else {
		run()
	}
	return done
}

// queueOpenCodeTurn keeps later prompts in the daemon until the current turn
// hands back or becomes idle. This keeps them out of a process being ended.
func (d *Daemon) queueOpenCodeTurn(w *worker, m protocol.Message) (queued, retry bool) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if d.workers[w.id] != w || w.stopping || w.doneStatus != "" {
		return false, true
	}
	if w.harness != "opencode" && w.harness != "agy" {
		return false, false
	}
	if w.turnAwaitingStart && w.turnMessageID == m.ID {
		return true, false
	}
	if !w.turnActive {
		w.idleFollowUp = w.idleNoticeSent && m.From == w.owner
		d.resetIdleNoticeLocked(w)
		w.turnActive = true
		return false, false
	}
	for _, queued := range w.turnQueue {
		if queued.ID == m.ID {
			return true, false
		}
	}
	w.turnQueue = append(w.turnQueue, m)
	return true, false
}

func (d *Daemon) deliverQueuedWorkerTurn(m protocol.Message) {
	ctx := context.Background()
	// A hand-back can drain the queue before onDeliver has saved dispatched.
	// Wait for that push to finish before claiming the actual harness write.
	for {
		d.mu.Lock()
		done := d.inflight[m.ID]
		d.mu.Unlock()
		if done == nil {
			break
		}
		<-done
	}
	if !d.startDelivery(ctx, m) {
		return
	}
	defer d.releaseDelivery(m.ID)
	ls := d.sessionByID(m.To)
	if ls == nil {
		d.finishDelivery(ctx, m, Result{Detail: "worker session is gone"})
		return
	}
	d.noteAsk(m)
	res := d.deliverTo(context.Background(), ls, m)
	d.finishDelivery(ctx, m, res)
}

// An OpenCode turn that did not hand back can finish normally. Let the next
// queued prompt run, or mark the worker ready for the next message.
func (d *Daemon) openCodeTurnIdle(session string) bool {
	d.wmu.Lock()
	var next *protocol.Message
	pending := false
	for _, w := range d.workers {
		if w.sessionID != session || (w.harness != "opencode" && w.harness != "agy") || w.stopping || w.doneStatus != "" {
			continue
		}
		if w.turnHandbackIdle {
			w.turnHandbackIdle = false
			pending = true
			break
		}
		if w.turnAwaitingStart {
			pending = true
			break
		}
		if len(w.turnQueue) == 0 {
			w.turnActive = false
		} else {
			m := w.turnQueue[0]
			w.turnQueue = w.turnQueue[1:]
			w.turnAwaitingStart = true
			w.turnMessageID = m.ID
			pending = true
			next = &m
		}
		break
	}
	d.wmu.Unlock()
	if next != nil {
		d.goBackground(func() { d.deliverQueuedWorkerTurn(*next) })
	}
	return pending
}
