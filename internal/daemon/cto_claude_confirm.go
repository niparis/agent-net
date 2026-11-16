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

// A message written to the Claude CTO's inbox is only accepted by the socket:
// on 2026-11-16 the running CTO accepted the owner's message and never started
// a turn for it. After each delivery agent-net watches the CTO's transcript
// for the message ID. If the CTO sits idle without taking the message up, it
// delivers once more, and then restarts the conversation with the message as
// its first prompt.

// ctoConfirmWaitDefault is how long an idle CTO may leave a message untaken.
// Claude Code starts a turn for an inbox line within a second or two; time
// while the CTO is busy with a turn does not count, since Claude holds the
// message until that turn ends.
const ctoConfirmWaitDefault = 20 * time.Second

// ctoMessageIDKey carries the network message ID of a delivery to the CTO,
// so the frame and the log lines name the message the owner sees.
type ctoMessageIDKey struct{}

func withCTOMessageID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctoMessageIDKey{}, id)
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

type ctoTurn int

const (
	ctoTurnTaken    ctoTurn = iota
	ctoTurnNotTaken         // idle for the whole wait without the message
	ctoTurnGone             // the CTO stopped or runs another conversation
	ctoTurnStopped          // the daemon is stopping
)

// confirmClaudeCTODelivery starts the check of one delivery. The caller has
// just written msgID to the inbox of w.session.
func (d *Daemon) confirmClaudeCTODelivery(w *ctoTranscriptWatch, msgID, text string) {
	if d.cfg.testNoCTOConfirm || d.ctoCtx == nil {
		return
	}
	ctx := d.ctoCtx
	d.goBackground(func() {
		for attempt := 1; ; attempt++ {
			switch d.waitClaudeCTOTurn(ctx, w) {
			case ctoTurnTaken:
				d.Logf("cto.delivery_confirmed msg=%s attempt=%d", msgID, attempt)
				return
			case ctoTurnGone:
				d.Logf("cto.delivery_unconfirmed msg=%s reason=cto_changed", msgID)
				return
			case ctoTurnStopped:
				return
			}
			if attempt == 1 {
				if !d.redeliverClaudeCTO(ctx, w, msgID, text) {
					return
				}
				continue
			}
			d.restartClaudeCTOForDelivery(ctx, w, msgID, text)
			return
		}
	})
}

func (d *Daemon) waitClaudeCTOTurn(ctx context.Context, w *ctoTranscriptWatch) ctoTurn {
	wait := d.cfg.ctoConfirmWait
	if wait <= 0 {
		wait = ctoConfirmWaitDefault
	}
	tick := time.NewTicker(min(250*time.Millisecond, wait/4))
	defer tick.Stop()
	var idle time.Duration
	last := time.Now()
	for {
		if w.seen() {
			return ctoTurnTaken
		}
		pid, ok := d.claudeCTOPID(w.session)
		if !ok {
			return ctoTurnGone
		}
		now := time.Now()
		if claudeSessionStatus(d.cfg.ClaudeSessionsDir, pid) != "busy" {
			idle += now.Sub(last)
		}
		last = now
		if idle >= wait {
			return ctoTurnNotTaken
		}
		select {
		case <-ctx.Done():
			return ctoTurnStopped
		case <-tick.C:
		}
	}
}

// claudeCTOPID is the process of the CTO's Claude while it runs session.
func (d *Daemon) claudeCTOPID(session string) (int, bool) {
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	root := d.claudeCTO.root.Load()
	if root == nil || d.claudeCTO.session != session {
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

// redeliverClaudeCTO writes the message to the inbox once more. It reports
// whether the check goes on.
func (d *Daemon) redeliverClaudeCTO(ctx context.Context, w *ctoTranscriptWatch, msgID, text string) bool {
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
	d.Logf("cto.delivery_retry msg=%s", msgID)
	deliver, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// A failed write needs no own step: the next wait sees no turn, and the
	// restart delivers it.
	if res := a.DeliverCTO(deliver, protocol.SessionInfo{Harness: "claude", NativeID: w.session}, msgID, text); !res.Delivered {
		d.Logf("cto.delivery_retry_failed msg=%s detail=%q", msgID, res.Detail)
	}
	return true
}

// restartClaudeCTOForDelivery ends the CTO's Claude job and resumes its
// conversation with the message as the first prompt. A prompt on the command
// line starts a turn; it does not depend on the inbox.
func (d *Daemon) restartClaudeCTOForDelivery(ctx context.Context, w *ctoTranscriptWatch, msgID, text string) {
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
	if d.claudeCTO.root.Load() == nil || d.claudeCTO.session != w.session {
		d.cto.mu.Unlock()
		d.Logf("cto.delivery_unconfirmed msg=%s reason=cto_changed", msgID)
		return
	}
	d.Logf("cto.restart_for_delivery msg=%s", msgID)
	d.stopClaudeCTOLocked("delivery_unconfirmed")
	d.cto.mu.Unlock()
	if err := d.startClaudeRoleWith(ctx, roleCTO, w.session, settings.ClaudeModel, text); err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, claudeCTOErrorOutput([]byte(err.Error())))
		return
	}
	d.cto.mu.Lock()
	_, err = d.saveClaudeCTOSessionLocked(w.session)
	d.cto.mu.Unlock()
	if err != nil {
		d.Logf("cto.restart_for_delivery msg=%s save_error=%q", msgID, err.Error())
	}
}
