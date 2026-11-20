package daemon

import (
	"context"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/providererr"
	"github.com/niparis/agent-net/internal/redact"
)

// Provider usage limits. When a harness reports a turn that failed with a
// usage or rate limit, the session carries limited_until and limited_reason
// to the gateway until the time passes or it completes a turn. Pi and
// OpenCode report through their plugins (turn_error, turn_ok and the status
// changes), Codex through its turn/completed notification, and Claude Code
// through its transcript (see internal/daemon/claude). All of them use
// providererr, the same classifier as the worker failure check.

// turnFailed records the text of a failed turn. A usage limit sets the limit
// on the session. Any failed turn stops the idle change after it from
// counting as a success, keeps its cause on the session, and tells the senders
// of the messages that were meant for the turn.
func (d *Daemon) turnFailed(ls *localSession, text string) {
	d.turnFailedAt(ls, text, time.Now())
}

func (d *Daemon) turnFailedAt(ls *localSession, text string, at time.Time) {
	if len(text) > 2000 {
		text = text[:2000]
	}
	l, limited := providererr.Limit(text, at)
	// The cause leaves the machine and goes to every TUI and to the sender.
	cause := protocol.CleanWaitingText(redact.Secrets(text))
	if cause == "" {
		cause = "unknown error"
	}
	d.mu.Lock()
	ls.turnErrored = true
	changed := ls.turnError != cause
	ls.turnError, ls.turnErrorAt = cause, at.Unix()
	if limited && (ls.limitUntil != l.Until.Unix() || ls.limitReason != l.Reason) {
		ls.limitUntil, ls.limitReason = l.Until.Unix(), l.Reason
		changed = true
	}
	owed := ls.takeOwedMessagesLocked()
	d.mu.Unlock()
	if changed {
		d.pushSession(ls)
	}
	d.reportNotDelivered(owed, "the session's turn failed: "+cause)
}

// turnSucceeded clears the limit: the provider answered a turn.
func (d *Daemon) turnSucceeded(ls *localSession) {
	d.mu.Lock()
	ls.turnErrored = false
	changed := ls.limitUntil != 0 || ls.turnError != ""
	ls.limitUntil, ls.limitReason = 0, ""
	ls.turnError, ls.turnErrorAt = "", 0
	ls.owedInTurn = nil
	d.mu.Unlock()
	if changed {
		d.pushSession(ls)
	}
}

// turnStartedLocked forgets the failure of the turn before. A message
// delivered earlier now has its turn. The caller holds d.mu.
func (ls *localSession) turnStartedLocked() {
	ls.turnErrored = false
	ls.turnStarts++
	ls.owedInTurn = append(ls.owedInTurn, ls.owedBeforeTurn...)
	ls.owedBeforeTurn = nil
}

// turnIdleLocked is a busy session going idle: a turn that reported no error
// ended, so the limit and the failure it showed are over. It reports whether
// anything was cleared. The caller holds d.mu.
func (ls *localSession) turnIdleLocked() bool {
	if ls.turnErrored {
		return false
	}
	ls.owedInTurn = nil
	if ls.limitUntil == 0 && ls.turnError == "" {
		return false
	}
	ls.limitUntil, ls.limitReason = 0, ""
	ls.turnError, ls.turnErrorAt = "", 0
	return true
}

// maxOwedMessages bounds the list of delivered messages a session keeps.
const maxOwedMessages = 20

// noteOwedLocked remembers that a message reached the session and has no turn
// yet. The caller holds d.mu.
func (ls *localSession) noteOwedLocked(msgID string) {
	if len(ls.owedBeforeTurn) >= maxOwedMessages {
		ls.owedBeforeTurn = ls.owedBeforeTurn[1:]
	}
	ls.owedBeforeTurn = append(ls.owedBeforeTurn, msgID)
}

// takeOwedMessagesLocked returns the messages that were meant for the failed
// turn, and forgets them: each is told once. The caller holds d.mu.
func (ls *localSession) takeOwedMessagesLocked() []string {
	owed := append(ls.owedInTurn, ls.owedBeforeTurn...)
	ls.owedInTurn, ls.owedBeforeTurn = nil, nil
	return owed
}

// reportNotDelivered asks the gateway to tell the sender of each message that
// it was not delivered. The gateway tells a sender once per message. It holds
// no lock of the daemon.
func (d *Daemon) reportNotDelivered(msgIDs []string, reason string) {
	for _, id := range msgIDs {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := d.call(ctx, "delivery.not_delivered", protocol.DeliveryUpdate{MessageID: id, State: protocol.StateFailed, Detail: reason})
		cancel()
		if err != nil {
			d.Logf("delivery.not_delivered %s: %v", id, err)
		}
	}
}
