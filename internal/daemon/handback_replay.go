package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A complete hand-back carries what only this daemon knows: the report, the
// file snapshots, the head and where the notice goes. A restart or a lost
// gateway link must not lose it. On the gateway machine the installer
// restarts the gateway first and the daemon a second later, so a hand-back
// that ends during an upgrade always finds the link down.
//
// An older daemon saved a hand-back in the state database before running the
// project's pre-hand-back check, and removed the row once its notice went out.
// That check is gone (decision 2026-11-14), but the rows it left and the
// replay path are still read: the next connect records the change and sends
// the notice, then removes the row.

// pendingHandback is the saved form of a handbackTail.
type pendingHandback struct {
	Req         protocol.WorkerHandbackRequest `json:"req"`
	To          string                         `json:"to,omitempty"`
	ReplyTo     string                         `json:"reply_to,omitempty"`
	SandboxNote string                         `json:"sandbox_note,omitempty"`
	End         bool                           `json:"end"`
	Files       *OutgoingFiles                 `json:"files,omitempty"`
	SessionID   string                         `json:"session_id"`
	Committed   string                         `json:"committed,omitempty"`
	WIPDetail   string                         `json:"wip_detail,omitempty"`
	Head        string                         `json:"head"`
	NoticeKey   string                         `json:"notice_key"`
	// Check is the hand-back check of an older daemon, kept so an upgrade
	// still reads the row. New hand-backs carry none (decision 2026-11-14).
	Check           *protocol.HandbackCheck `json:"check,omitempty"`
	Recorded        bool                    `json:"recorded,omitempty"`
	ChangeID        string                  `json:"change_id,omitempty"`
	DeliveryProblem string                  `json:"delivery_problem,omitempty"`
	Stats           *workerStats            `json:"stats,omitempty"`
}

// errHandbackDeferred ends a hand-back that is saved for the next connect.
var errHandbackDeferred = errors.New("the hand-back is saved; the next connection to the gateway records it and sends the notice")

func (t *handbackTail) saved() pendingHandback {
	return pendingHandback{Req: t.req, To: t.to, ReplyTo: t.replyTo, SandboxNote: t.sandboxNote, End: t.end, Files: t.files,
		SessionID: t.sessionID, Committed: t.committed, WIPDetail: t.wipDetail, Head: t.head, NoticeKey: t.noticeKey,
		Check: t.check, Recorded: t.recorded, ChangeID: t.changeID, DeliveryProblem: t.deliveryProblem, Stats: t.stats}
}

func (p pendingHandback) tail() *handbackTail {
	return &handbackTail{req: p.Req, to: p.To, replyTo: p.ReplyTo, sandboxNote: p.SandboxNote, end: p.End, files: p.Files,
		sessionID: p.SessionID, committed: p.Committed, wipDetail: p.WIPDetail, complete: true,
		head: p.Head, noticeKey: p.NoticeKey, persisted: true, check: p.Check,
		recorded: p.Recorded, changeID: p.ChangeID, deliveryProblem: p.DeliveryProblem, stats: p.Stats}
}

// persistHandback saves a complete hand-back for the next connect. An
// Antigravity hand-back is left out: its stream state already carries the
// hand-back across a restart (worker_agy_stream.go).
func (d *Daemon) persistHandback(w *worker, t *handbackTail, head string) {
	if w.harness == "agy" {
		return
	}
	t.head, t.noticeKey = head, protocol.NewID("k")
	if err := d.saveHandback(w.id, t); err != nil {
		d.Logf("worker %s: save the hand-back: %v", w.id, err)
		return
	}
	t.persisted = true
}

func (d *Daemon) saveHandback(workerID string, t *handbackTail) error {
	raw, err := json.Marshal(t.saved())
	if err != nil {
		return err
	}
	_, err = d.st.db.Exec(`INSERT INTO worker_pending_handback(worker_id,head,payload,at) VALUES(?,?,?,?)
 ON CONFLICT(worker_id) DO UPDATE SET head=excluded.head, payload=excluded.payload, at=excluded.at`,
		workerID, t.head, string(raw), time.Now().Unix())
	return err
}

// dropHandback removes the saved hand-back of one head. A newer hand-back on
// another head has replaced the row and keeps it.
func (d *Daemon) dropHandback(workerID, head string) {
	if _, err := d.st.db.Exec(`DELETE FROM worker_pending_handback WHERE worker_id=? AND head=?`, workerID, head); err != nil {
		d.Logf("worker %s: remove the saved hand-back: %v", workerID, err)
	}
}

// deferHandback keeps the hand-back, with what it knows so far, for the next
// connect.
func (d *Daemon) deferHandback(w *worker, t *handbackTail, why string) error {
	if err := d.saveHandback(w.id, t); err != nil {
		d.Logf("worker %s: save the hand-back: %v", w.id, err)
	}
	d.Logf("worker.handback_saved worker=%s head=%s recorded=%t reason=%q", w.id, t.head, t.recorded, why)
	return errHandbackDeferred
}

func (s *state) pendingHandbacks() (map[string]pendingHandback, error) {
	rows, err := s.db.Query(`SELECT worker_id, payload FROM worker_pending_handback`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]pendingHandback{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var p pendingHandback
		if json.Unmarshal([]byte(raw), &p) == nil {
			out[id] = p
		}
	}
	return out, rows.Err()
}

func (s *state) pendingHandback(workerID string) (pendingHandback, bool) {
	var raw string
	if err := s.db.QueryRow(`SELECT payload FROM worker_pending_handback WHERE worker_id=?`, workerID).Scan(&raw); err != nil {
		return pendingHandback{}, false
	}
	var p pendingHandback
	return p, json.Unmarshal([]byte(raw), &p) == nil
}

// replayHandbacks records and announces the hand-backs that a stop or a lost
// link left saved, including rows an older daemon's check left behind. The
// link calls it after each connect. A row stays until its notice is sent, so
// a replay that loses the link again is tried at the next connect.
func (d *Daemon) replayHandbacks() {
	saved, err := d.st.pendingHandbacks()
	if err != nil {
		d.Logf("saved hand-backs: %v", err)
		return
	}
	for id, p := range saved {
		if !d.claimHandbackReplay(id) {
			continue
		}
		if !d.goBackground(func() {
			defer d.releaseHandbackReplay(id)
			d.replayHandback(id, p)
		}) {
			d.releaseHandbackReplay(id)
		}
	}
}

func (d *Daemon) claimHandbackReplay(id string) bool {
	d.reportMu.Lock()
	defer d.reportMu.Unlock()
	if d.replaying[id] {
		return false
	}
	if d.replaying == nil {
		d.replaying = map[string]bool{}
	}
	d.replaying[id] = true
	return true
}

func (d *Daemon) releaseHandbackReplay(id string) {
	d.reportMu.Lock()
	delete(d.replaying, id)
	d.reportMu.Unlock()
}

func (d *Daemon) replayHandback(workerID string, p pendingHandback) {
	t := p.tail()
	d.wmu.Lock()
	w := d.workers[workerID]
	if w != nil && w.stopping {
		t.end = false
	} else if w != nil {
		w.handbackPending = true
	}
	d.wmu.Unlock()
	if w == nil {
		// The worker process ended while the daemon was down. Its run is over,
		// so the notice is all that is left to do.
		w = d.endedHandbackWorker(p.SessionID)
		if w == nil {
			d.Logf("worker.handback_dropped worker=%s reason=%q", workerID, "no record of the worker")
			d.dropHandback(workerID, p.Head)
			p.Files.Remove()
			return
		}
		t.end = false
	}
	d.Logf("worker.handback_replay worker=%s head=%s", workerID, p.Head)
	_, err := d.finishHandback(context.Background(), w, t)
	deferred := errors.Is(err, errHandbackDeferred)
	d.wmu.Lock()
	if cur := d.workers[workerID]; cur != nil {
		cur.handbackPending = deferred
	}
	d.wmu.Unlock()
	if deferred {
		return
	}
	if err != nil {
		d.Logf("worker %s: saved hand-back: %v", workerID, err)
	}
	p.Files.Remove()
}

// endedHandbackWorker is the worker of an ended run, read from its row, for a
// saved hand-back whose notice is still to be sent. It has no process, so the
// hand-back saves nothing over the row and ends nothing.
func (d *Daemon) endedHandbackWorker(sessionID string) *worker {
	r, ok, err := d.st.workerBySession(sessionID)
	if err != nil || !ok {
		return nil
	}
	return &worker{runGeneration: r.RunGeneration, evidence: r.Evidence, id: r.ID, harness: r.Harness, project: r.Project, path: r.Path, model: r.Model,
		owner: r.Owner, ownerName: r.OwnerName, sessionID: r.SessionID, startedAt: r.StartedAt, taskMsg: r.TaskMsg, mainPath: r.MainPath,
		worktree: r.Worktree, branch: r.Branch, base: r.Base, title: r.Title, changeID: r.ChangeID, continueCount: r.Continue, fixCount: r.Fix,
		autoContinueCount: r.AutoContinueCount, difficultyAsked: r.DifficultyAsked, difficultyUsed: r.DifficultyUsed, permissions: protocol.WorkerPermissionsOrDefault(r.Permissions)}
}

// adoptSavedHandback prepares a lost worker's row for its saved hand-back: the
// worker handed back complete, so its run ended after a hand-back and its
// session can start again, as if its notice had gone out before the stop.
func adoptSavedHandback(r *workerRow, p pendingHandback) {
	r.DoneStatus, r.DonePartial = p.Req.Status, false
	r.EndedReason, r.Resumable = protocol.StopHandback, true
}
