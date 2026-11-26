package daemon

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/niparis/agent-net/internal/daemon/opencodeapi"
)

// The OpenCode CTO's delivery check (decision 0171, part b). The check reads the
// newest messages once: a message that is not the owner's, created at or after
// the prompt, means a turn started. A turn that fails before the model answers
// still ends with such a message (an idle one), so it counts as taken. A busy
// session is held, not redelivered to.

// watchOpenCodeCTODelivery starts the check of one prompt sent to the CTO's
// conversation. sent is the time the prompt was created, in milliseconds, and
// sentID is the ID of its message in the conversation.
func (d *Daemon) watchOpenCodeCTODelivery(id, base, pass, version, identity string, sent int64, sentID, text, msgID string) {
	// The IDs of every copy of the prompt sent. A restart checks them, so a copy
	// that is already in the conversation is not sent again.
	var ids []string
	if sentID != "" {
		ids = append(ids, sentID)
	}
	sentIDs := &ids
	// current reports whether the CTO still runs the conversation on the server
	// that took the prompt. A restart changes the server, which ends the check.
	current := func() bool {
		d.cto.mu.Lock()
		defer d.cto.mu.Unlock()
		return d.cto.session == id && d.cto.base == base
	}
	d.confirmDelivery("cto", deliveryWatch{
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
			return d.redeliverOpenCodeCTO(ctx, current, base, pass, version, identity, id, text, msgID, sentIDs)
		},
		restart: func(ctx context.Context) { d.restartOpenCodeCTOForDelivery(ctx, id, text, msgID, *sentIDs) },
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
func (d *Daemon) redeliverOpenCodeCTO(ctx context.Context, current func() bool, base, pass, version, identity, id, text, msgID string, sentIDs *[]string) bool {
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
	raw, err := d.cto.apiTo(ctx, base, pass, opencodeapi.Prompt.Method(), path, map[string]string{"text": text, "delivery": "queue"})
	if err != nil {
		d.Logf("cto.delivery_retry_failed msg=%s detail=%q", msgID, err.Error())
		return true
	}
	if sid := promptMessageID(raw); sid != "" {
		*sentIDs = append(*sentIDs, sid)
	}
	return true
}

// promptMessageID is the ID of the message a prompt answer names, or "".
func promptMessageID(raw []byte) string {
	var answer struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &answer) != nil {
		return ""
	}
	return answer.Data.ID
}

// openCodeNudgeText is the turn a restart sends for a stored prompt that has no
// reply. It points at the message, so the full text is not sent again.
const openCodeNudgeText = "Answer the message above that has no reply yet."

// openCodePromptState says what the conversation holds for a sent prompt.
type openCodePromptState int

const (
	// promptNotStored: no copy of the prompt is in the conversation.
	promptNotStored openCodePromptState = iota
	// promptUnanswered: a copy is stored and no assistant message came after it.
	promptUnanswered
	// promptAnswered: a copy is stored and an assistant message came after it.
	promptAnswered
)

// openCodePromptState reads the newest messages once. An error means the state is
// unknown, and the caller must not send anything on a guess.
func (d *Daemon) openCodePromptState(ctx context.Context, base, pass, version, identity, id string, sentIDs []string) (openCodePromptState, error) {
	if len(sentIDs) == 0 {
		return promptNotStored, nil
	}
	routes, err := d.discoverOpenCodeRoutes(ctx, base, pass, version, identity)
	if err != nil {
		return promptNotStored, err
	}
	path, err := routes.Path(opencodeapi.Messages, id)
	if err != nil {
		return promptNotStored, err
	}
	raw, err := d.cto.apiTo(ctx, base, pass, opencodeapi.Messages.Method(), path+"?order=desc&limit=200", nil)
	if err != nil {
		return promptNotStored, err
	}
	var page struct {
		Data []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			Time struct {
				Created int64 `json:"created"`
			} `json:"time"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return promptNotStored, err
	}
	// The list is newest first. The newest stored copy of the prompt is the one to judge.
	at := -1
	for i, m := range page.Data {
		if slices.Contains(sentIDs, m.ID) {
			at = i
			break
		}
	}
	if at < 0 {
		return promptNotStored, nil
	}
	since := page.Data[at].Time.Created
	for _, m := range page.Data[:at] {
		if m.Type == "assistant" && m.Time.Created >= since {
			return promptAnswered, nil
		}
	}
	return promptUnanswered, nil
}

// restartOpenCodeCTOForDelivery ends the CTO's private server, starts it again
// and sends the prompt to the same conversation. OpenCode keeps the conversation
// on disk, so the new server answers in it. The first copy of the prompt stays
// in the conversation, so the owner's message can appear twice in its history.
func (d *Daemon) restartOpenCodeCTOForDelivery(ctx context.Context, id, text, msgID string, sentIDs []string) {
	if err := d.ctoDispatchMu.lock(ctx); err != nil {
		return
	}
	defer d.ctoDispatchMu.unlock()
	c := &d.cto
	c.mu.Lock()
	if c.session != id {
		c.mu.Unlock()
		d.Logf("cto.delivery_unconfirmed msg=%s reason=cto_changed", msgID)
		return
	}
	d.stopOpenCodeCTOLocked("delivery_unconfirmed")
	c.mu.Unlock()
	if err := d.startOpenCodeRole(ctx, roleCTO); err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, err.Error())
		return
	}
	c.mu.Lock()
	gen := c.gen
	base, password, version, identity := c.base, c.password, c.version, c.identity
	memSession, memModel := c.session, c.model
	c.mu.Unlock()
	if base == "" {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, errCTONotRunning.Error())
		return
	}
	if _, err := d.syncOpenCodeCTOSelectionAt(ctx, gen, base, password, version, identity, memSession, memModel, id); err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, err.Error())
		return
	}
	state, err := d.openCodePromptState(ctx, base, password, version, identity, id, sentIDs)
	if err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=stored_unknown detail=%q", msgID, err.Error())
		return
	}
	switch state {
	case promptAnswered:
		// The conversation answered the prompt already. The restart does nothing more.
		d.Logf("cto.delivery_resumed msg=%s", msgID)
		return
	case promptUnanswered:
		// The prompt is stored with no reply. Nudge the conversation to answer it,
		// rather than sending the full text again.
		if _, err := d.ctoOpAt(ctx, base, password, version, identity, opencodeapi.Prompt, id, "", map[string]string{"text": openCodeNudgeText, "delivery": "queue"}); err != nil {
			d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, err.Error())
			return
		}
		d.Logf("cto.delivery_nudged msg=%s", msgID)
		return
	}
	if _, err := d.ctoOpAt(ctx, base, password, version, identity, opencodeapi.Prompt, id, "", map[string]string{"text": text, "delivery": "queue"}); err != nil {
		d.Logf("cto.delivery_unconfirmed msg=%s reason=restart_failed detail=%q", msgID, err.Error())
	}
}

// noteCTOCompacted records a compaction of the CTO's OpenCode conversation. The
// next turn carries the start block again.
func (d *Daemon) noteCTOCompacted(session, reason string) {
	d.noteCompacted("opencode", session, d.ctoNowTime().Unix(), reason)
	d.markStartContextDue(d.ctoSessionID())
}
