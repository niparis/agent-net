package daemon

import (
	"context"
	"encoding/json"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
)

// The OpenCode CTO's delivery check (decision 0171, part b). The check reads the
// newest messages once: a message that is not the owner's, created at or after
// the prompt, means a turn started. A turn that fails before the model answers
// still ends with such a message (an idle one), so it counts as taken. A busy
// session is held, not redelivered to.

// watchOpenCodeCTODelivery starts the check of one prompt sent to the CTO's
// conversation. sent is the time the prompt was created, in milliseconds.
func (d *Daemon) watchOpenCodeCTODelivery(id, base, pass, version, identity string, sent int64, text, msgID string) {
	// current reports whether the CTO still runs the conversation on the server
	// that took the prompt. A restart changes the server, which ends the check.
	current := func() bool {
		d.cto.mu.Lock()
		defer d.cto.mu.Unlock()
		return d.cto.session == id && d.cto.base == base
	}
	d.confirmDelivery(deliveryWatch{
		label: "cto",
		msgID: msgID,
		gone:  func() bool { return !current() },
		busy: func(ctx context.Context) bool {
			d.countDeliveryRead()
			busy, known := d.openCodeSessionBusy(ctx, base, pass, version, identity, id)
			return known && busy
		},
		taken: func(ctx context.Context) bool {
			return d.openCodeTurnSince(ctx, base, pass, version, identity, id, sent)
		},
		redeliver: func(ctx context.Context) bool {
			return d.redeliverOpenCodeCTO(ctx, current, base, pass, version, identity, id, text, msgID)
		},
		restart: func(ctx context.Context) { d.restartOpenCodeCTOForDelivery(ctx, id, text, msgID) },
	})
}

// openCodeTurnSince reads the newest messages of the conversation once. It
// reports whether one that is not the owner's was created at or after since.
func (d *Daemon) openCodeTurnSince(ctx context.Context, base, pass, version, identity, id string, since int64) bool {
	routes, err := d.discoverOpenCodeRoutes(ctx, base, pass, version, identity)
	if err != nil {
		return false
	}
	path, err := routes.Path(opencodeapi.Messages, id)
	if err != nil {
		return false
	}
	d.countDeliveryRead()
	raw, err := d.cto.apiTo(ctx, base, pass, opencodeapi.Messages.Method(), path+"?order=desc&limit=10", nil)
	if err != nil {
		return false
	}
	var page struct {
		Data []struct {
			Type string `json:"type"`
			Time struct {
				Created int64 `json:"created"`
			} `json:"time"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &page) != nil {
		return false
	}
	for _, m := range page.Data {
		if m.Type != "user" && m.Time.Created >= since {
			return true
		}
	}
	return false
}

// redeliverOpenCodeCTO sends the prompt once more to the server that took the
// first one. It reports whether the check goes on.
func (d *Daemon) redeliverOpenCodeCTO(ctx context.Context, current func() bool, base, pass, version, identity, id, text, msgID string) bool {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return false
	}
	defer d.ctoDispatchMu.unlock()
	if !current() {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=cto_changed", msgID)
		return false
	}
	path, err := d.openCodePath(ctx, base, pass, version, identity, opencodeapi.Prompt, id)
	if err != nil {
		d.Logf("cto.delivery_retry_failed msg=%s detail=%q", msgID, err.Error())
		return false
	}
	if _, err := d.cto.apiTo(ctx, base, pass, opencodeapi.Prompt.Method(), path, map[string]string{"text": text, "delivery": "queue"}); err != nil {
		d.Logf("cto.delivery_retry_failed msg=%s detail=%q", msgID, err.Error())
	}
	return true
}

// restartOpenCodeCTOForDelivery ends the CTO's private server, starts it again
// and sends the prompt to the same conversation. OpenCode keeps the conversation
// on disk, so the new server answers in it. The first copy of the prompt stays
// in the conversation, so the owner's message can appear twice in its history.
func (d *Daemon) restartOpenCodeCTOForDelivery(ctx context.Context, id, text, msgID string) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return
	}
	defer d.ctoDispatchMu.unlock()
	c := &d.cto
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != id {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=cto_changed", msgID)
		return
	}
	d.stopOpenCodeCTOLocked("delivery_unconfirmed")
	if err := d.startOpenCodeRole(ctx, roleCTO); err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, err.Error())
		return
	}
	if _, err := d.syncOpenCodeCTOSelection(ctx, id); err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, err.Error())
		return
	}
	if _, err := d.ctoOp(ctx, opencodeapi.Prompt, id, "", map[string]string{"text": text, "delivery": "queue"}); err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, err.Error())
	}
}

// noteCTOCompacted records a compaction of the CTO's OpenCode conversation. The
// next turn carries the start block again.
func (d *Daemon) noteCTOCompacted(session, reason string) {
	d.noteCompacted("opencode", session, d.ctoNowTime().Unix(), reason)
	d.markStartContextDue(d.ctoSessionID())
}
