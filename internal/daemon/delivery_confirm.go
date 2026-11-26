package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The delivery confirm (decision 0171, part b). A message the daemon delivered
// to a session should start a turn. When none starts within the wait, the daemon
// delivers once more, then restarts the session with the message as its first
// prompt. On 2026-11-16 the running CTO's inbox accepted a message and Claude
// never started a turn for it, which is why the check exists.
//
// Each check reads what the harness reports, once: the Claude transcript, the
// OpenCode conversation, or the queued-message notice that Codex sends. A session
// that is busy at a check is not redelivered to, because its harness takes the
// message when the turn ends. The check waits again on the same cadence, so
// nothing spins.

// ctoMessageIDKey carries the network message ID of a delivery to the CTO, so
// the frame and the log lines name the message the owner sees.
type ctoMessageIDKey struct{}

func withCTOMessageID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctoMessageIDKey{}, id)
}

// deliveryConfirmWaitDefault is how long a session may leave a delivered
// message without starting a turn before the daemon acts.
const deliveryConfirmWaitDefault = 20 * time.Second

// deliveryWatch is the check of one delivered message. The closures do the work
// of one harness; the loop in runDeliveryWatch decides what to do with the answers.
type deliveryWatch struct {
	// label prefixes the log lines: "cto" for the CTO, "session" for the rest.
	label string
	msgID string
	// gone reports that the session no longer runs the conversation that took
	// the delivery.
	gone func() bool
	// busy reports that the session runs a turn now.
	busy func(ctx context.Context) bool
	// taken reports that a turn started for the message since the delivery.
	taken func(ctx context.Context) bool
	// redeliver sends the message once more. It reports whether the check goes on.
	redeliver func(ctx context.Context) bool
	// restart starts the session with the message as its first prompt. Nil when
	// the harness has no restart.
	restart func(ctx context.Context)
	// unconfirmed runs when a harness with no restart still shows no turn after
	// the redelivery. Nil when nothing is done then.
	unconfirmed func(ctx context.Context)
	// done runs when the check ends, however it ends. Nil when nothing is kept.
	done func()
}

// pendingCheck is the one delivery check a session has at a time.
type pendingCheck struct {
	cancel context.CancelFunc
}

// confirmDelivery starts the check of one delivered message, in the background. A
// session has one check at a time: the newest message is the one that must be taken,
// so its check replaces the older one. The check ends when the message is taken, when
// the session goes offline, ends or is archived, when agent-net stops it, or when the
// daemon stops. It does nothing in a test that turns the check off, or before the
// daemon's context exists.
func (d *Daemon) confirmDelivery(key string, w deliveryWatch) {
	if d.cfg.testNoDeliveryConfirm || d.ctoCtx == nil {
		if w.done != nil {
			w.done()
		}
		return
	}
	ctx, cancel := context.WithCancel(d.ctoCtx)
	p := &pendingCheck{cancel: cancel}
	d.checksMu.Lock()
	if d.checks == nil {
		d.checks = map[string]*pendingCheck{}
	}
	if prev := d.checks[key]; prev != nil {
		prev.cancel()
	}
	d.checks[key] = p
	d.checksMu.Unlock()
	end := func() {
		d.checksMu.Lock()
		if d.checks[key] == p {
			delete(d.checks, key)
		}
		d.checksMu.Unlock()
		cancel()
	}
	if !d.goBackground(func() {
		defer end()
		d.runDeliveryWatch(ctx, w)
	}) {
		end()
		if w.done != nil {
			w.done()
		}
	}
}

// runDeliveryWatch is the one sequence of checks for every harness. Only a
// check that finds the session idle and the message untaken goes on to the next
// step: one redelivery, then a restart.
func (d *Daemon) runDeliveryWatch(ctx context.Context, w deliveryWatch) {
	d.checkRuns.Add(1)
	defer d.checkRuns.Add(-1)
	if w.done != nil {
		defer w.done()
	}
	// attempt counts redeliveries only. A busy check waits again without counting.
	attempt := 1
	for {
		if !d.waitDeliveryCheck(ctx) {
			return
		}
		if w.gone() {
			d.Logf("%s.delivery_unconfirmed msg=%s reason=cto_changed", w.label, w.msgID)
			return
		}
		if w.taken(ctx) {
			d.Logf("%s.delivery_confirmed msg=%s attempt=%d", w.label, w.msgID, attempt)
			return
		}
		if w.busy(ctx) {
			continue
		}
		if attempt == 1 {
			d.Logf("%s.delivery_retry msg=%s", w.label, w.msgID)
			if !w.redeliver(ctx) {
				return
			}
			attempt = 2
			continue
		}
		if w.restart == nil {
			d.Logf("%s.delivery_unconfirmed msg=%s reason=no_restart", w.label, w.msgID)
			if w.unconfirmed != nil {
				w.unconfirmed(ctx)
			}
			return
		}
		d.Logf("%s.restart_for_delivery msg=%s", w.label, w.msgID)
		w.restart(ctx)
		return
	}
}

// waitDeliveryCheck sleeps for one check interval. It reports false when the
// daemon stops first.
func (d *Daemon) waitDeliveryCheck(ctx context.Context) bool {
	wait := d.cfg.deliveryConfirmWait
	if wait <= 0 {
		wait = deliveryConfirmWaitDefault
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// countDeliveryRead counts one read of a harness made for a delivery check. A
// closure that reads the harness calls it; a check that reads nothing does not.
// The count is what the cost of the confirm is measured by.
func (d *Daemon) countDeliveryRead() {
	d.deliveryReads.Add(1)
}

// ctoTranscriptWatch finds a message ID in the bytes appended to the CTO's
// transcript after start. It never reads what was there before.
type ctoTranscriptWatch struct {
	projects, session string
	needle            []byte
	path              string
	offset            int64
}

func (w *ctoTranscriptWatch) find() string {
	if w.path == "" {
		if matches, _ := filepath.Glob(filepath.Join(w.projects, "*", w.session+".jsonl")); len(matches) > 0 {
			w.path = matches[0]
		}
	}
	return w.path
}

// start marks the current end of the transcript. A conversation without a
// transcript yet is read from its first byte.
func (w *ctoTranscriptWatch) start() {
	if path := w.find(); path != "" {
		if info, err := os.Stat(path); err == nil {
			w.offset = info.Size()
		}
	}
}

// seen reports whether the bytes added since the last call hold the needle.
func (w *ctoTranscriptWatch) seen() bool {
	path := w.find()
	if path == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false
	}
	if info.Size() < w.offset {
		w.offset = 0
	}
	if info.Size() == w.offset {
		return false
	}
	buf := make([]byte, info.Size()-w.offset)
	n, _ := f.ReadAt(buf, w.offset)
	if bytes.Contains(buf[:n], w.needle) {
		return true
	}
	// Keep a needle's length minus one: a line still being written may hold
	// the start of it.
	w.offset = max(w.offset, w.offset+int64(n)-int64(len(w.needle)-1))
	return false
}

// confirmClaudeCTODelivery starts the check of one delivery to the Claude CTO.
// The caller has just written msgID to the inbox of w.session. label is the
// sender's label: empty for the owner, and the label of the first delivery
// otherwise, so every retry keeps the same sender.
func (d *Daemon) confirmClaudeCTODelivery(w *ctoTranscriptWatch, msgID, label, text string) {
	d.confirmDelivery("cto", deliveryWatch{
		label: "cto",
		msgID: msgID,
		gone:  func() bool { _, ok := d.claudeCTOPID(w.session); return !ok },
		busy: func(ctx context.Context) bool {
			d.countDeliveryRead()
			pid, ok := d.claudeCTOPID(w.session)
			return ok && claudeSessionStatus(d.cfg.ClaudeSessionsDir, pid) == "busy"
		},
		taken: func(ctx context.Context) bool {
			d.countDeliveryRead()
			return w.seen()
		},
		redeliver: func(ctx context.Context) bool { return d.redeliverClaudeCTO(ctx, w, msgID, label, text) },
		restart:   func(ctx context.Context) { d.restartClaudeCTOForDelivery(ctx, w, msgID, label, text) },
	})
}

// claudeCTOPID is the process of the CTO's Claude while it runs session.
func (d *Daemon) claudeCTOPID(session string) (int, bool) {
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	root := d.claudeCTO.root.Load()
	if root == nil || d.claudeCTO.sessionID() != session {
		return 0, false
	}
	return root.pid, true
}

// claudeSessionStatus is the turn state Claude Code writes to the session
// file: "busy" while the model works.
func claudeSessionStatus(sessions string, pid int) string {
	raw, err := os.ReadFile(filepath.Join(sessions, strconv.Itoa(pid)+".json"))
	if err != nil {
		return ""
	}
	var sf claudeSessionFile
	if json.Unmarshal(raw, &sf) != nil {
		return ""
	}
	return sf.Status
}

// redeliverClaudeCTO writes the message to the inbox once more, with the frame
// of its first delivery. It reports whether the check goes on.
func (d *Daemon) redeliverClaudeCTO(ctx context.Context, w *ctoTranscriptWatch, msgID, label, text string) bool {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return false
	}
	defer d.ctoDispatchMu.unlock()
	// The CTO may have taken it up while this waited for the lock.
	if w.seen() {
		d.Logf("cto.delivery_confirmed msg=%s attempt=1", msgID)
		return false
	}
	if _, ok := d.claudeCTOPID(w.session); !ok {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=cto_changed", msgID)
		return false
	}
	a, err := d.claudeCTODeliverer()
	if err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=%q", msgID, err.Error())
		return false
	}
	deliver, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// A failed write needs no own step: the next check sees no turn, and the
	// restart delivers it. The label is the first delivery's, so a peer, a bot,
	// the CTO or an agent-net notice never becomes the owner's frame.
	var res Result
	if label == "" {
		res = a.DeliverCTO(deliver, protocol.SessionInfo{Harness: "claude", NativeID: w.session}, msgID, text)
	} else {
		res = a.DeliverCTOSystem(deliver, protocol.SessionInfo{Harness: "claude", NativeID: w.session}, msgID, label, text)
	}
	if !res.Delivered {
		d.Logf("cto.delivery_retry_failed msg=%s detail=%q", msgID, res.Detail)
	}
	return true
}

// restartClaudeCTOForDelivery ends the CTO's Claude job and resumes its
// conversation with the message as the first prompt. A prompt on the command
// line starts a turn; it does not depend on the inbox. The prompt is the same
// frame as the first delivery, so the restarted conversation sees the sender
// and the message ID, not bare text or the owner's frame.
func (d *Daemon) restartClaudeCTOForDelivery(ctx context.Context, w *ctoTranscriptWatch, msgID, label, text string) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return
	}
	defer d.ctoDispatchMu.unlock()
	if w.seen() {
		d.Logf("cto.delivery_confirmed msg=%s attempt=2", msgID)
		return
	}
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=settings detail=%q", msgID, err.Error())
		return
	}
	d.cto.mu.Lock()
	if d.claudeCTO.root.Load() == nil || d.claudeCTO.sessionID() != w.session {
		d.cto.mu.Unlock()
		d.Logf("cto.delivery_unconfirmed msg=%s reason=cto_changed", msgID)
		return
	}
	stop := d.detachClaudeCTOLocked("delivery_unconfirmed")
	d.cto.mu.Unlock()
	stop.run()
	if err := d.startClaudeRoleWith(ctx, roleCTO, w.session, settings.ClaudeModel, CTOFirstPrompt(msgID, label, text)); err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, claudeCTOErrorOutput([]byte(err.Error())))
		return
	}
	d.cto.mu.Lock()
	id := d.claudeCTO.sessionID()
	d.cto.mu.Unlock()
	err = d.saveClaudeCTOSession(w.session, id)
	if err != nil {
		d.Logf("cto.restart_for_delivery msg=%s save_error=%q", msgID, err.Error())
	}
}
