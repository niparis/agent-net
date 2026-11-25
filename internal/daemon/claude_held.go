package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Claude Code holds a message from another session for review when the
// sender's permission mode class differs from the receiver's. agent-net sees
// the write to the inbox succeed and used to report the message as delivered.
// The session's transcript names the hold, so the daemon reports it as held
// instead, and reports the release when the owner approves it.
//
// A report may not reach the gateway on its first try: the link can be down,
// or the daemon can stop. Each one is kept in daemon.db and sent again after
// the next connect and after a restart, in order, so a hold always arrives
// before the release that follows it.
//
// agent-net does not fake a matching mode, set crossSessionInbound, or send a
// held message again. The owner decides those.

const (
	reportHeld     = "delivery.held"
	reportRelease  = "delivery.released"
	releasedDetail = "released in Claude Code"
)

// claudeHeldDetail is what the sender and the TUI read about a held message.
func (d *Daemon) claudeHeldDetail() string {
	action := "in that Claude Code session, review and approve the held message"
	if path := d.claudeSettingsFile(); path != "" {
		action += fmt.Sprintf(", or set \"crossSessionInbound\": \"accept\" in %s", path)
	}
	return "held by Claude Code: " + action + ". The sender's permission mode class does not match this session's. " +
		"agent-net did not send it again. Claude Code writes no notice when it drops a held message, so this one may have expired there; " +
		"agent-net cannot see whether it still waits."
}

// ClaudeHeld records one hold notice that the session's transcript names. It
// marks the one delivery the notice describes; when that is not certain, it
// logs the notice and marks nothing.
func (d *Daemon) ClaudeHeld(ctx context.Context, recipient string, e ClaudeHeldEntry) {
	detail := d.claudeHeldDetail()
	r, ok, handled, err := d.st.markClaudeHeld(recipient, e, detail)
	if err != nil {
		d.Logf("claude: cannot record the held message in %s: %v", recipient, err)
		return
	}
	if handled {
		// A restart reads the transcript again; this notice was applied.
		return
	}
	if !ok {
		d.Logf("claude: a hold notice in %s for %q from %s matches no single unheld delivery; not marking any message", recipient, e.Preview, e.ReplySock)
		return
	}
	d.Logf("session.delivery_held msg=%s sender=%s recipient=%s detail=%q", r.MessageID, r.Sender, recipient, detail)
	if d.flushClaudeReports(ctx) {
		d.wakeOutbox()
	}
}

// ClaudeHeldReleased records that the session released the frame with this
// reply socket and content hash. The gateway moves it from held_by_session to
// delivered. A message ID never ties a release: the body and agent-net's cited
// notes can name other messages.
func (d *Daemon) ClaudeHeldReleased(ctx context.Context, recipient, replySock, frameHash string, at time.Time) {
	ids, err := d.st.releaseClaudeHeld(recipient, replySock, frameHash, at)
	if err != nil {
		d.Logf("claude: cannot record a released message in %s: %v", recipient, err)
		return
	}
	for _, id := range ids {
		d.Logf("session.delivery_released msg=%s", id)
	}
	if len(ids) > 0 && d.flushClaudeReports(ctx) {
		d.wakeOutbox()
	}
}

// flushClaudeReports sends the hold and release reports that wait, in their
// order. A link that is down or a gateway fault that may pass stops the flush
// and keeps the rest for the next connect or the outbox loop. A refusal is
// final and drops the report. It returns true when something was left.
func (d *Daemon) flushClaudeReports(ctx context.Context) (stalled bool) {
	rows, err := d.st.claudeReports()
	if err != nil {
		d.Logf("claude delivery reports: %v", err)
		return true
	}
	for _, r := range rows {
		if ctx.Err() != nil {
			return true
		}
		if !d.claimClaudeReport(r.Seq) {
			// Another flush is sending this report; later rows must wait for it.
			return true
		}
		ok := d.sendClaudeReport(ctx, r)
		d.releaseClaudeReport(r.Seq)
		if !ok {
			return true
		}
		if err := d.st.dropClaudeReport(r.Seq); err != nil {
			d.Logf("claude delivery report %d: %v", r.Seq, err)
			return true
		}
	}
	return false
}

// sendClaudeReport makes one gateway call. It reports whether the report is
// finished: accepted, or refused for good. A call that did not get through
// stays for the next try.
func (d *Daemon) sendClaudeReport(ctx context.Context, r claudeReport) bool {
	state := protocol.StateDelivered
	if r.Kind == reportHeld {
		state = protocol.StateHeldBySession
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err := d.call(callCtx, r.Kind, protocol.DeliveryUpdate{MessageID: r.MessageID, State: state, Detail: r.Detail})
	cancel()
	if err == nil {
		return true
	}
	var refused RemoteError
	if errors.As(err, &refused) && !errors.Is(err, errGatewayRetry) {
		d.Logf("claude delivery report %s: refused: %v", r.MessageID, err)
		return true
	}
	d.Logf("claude delivery report %s: %v", r.MessageID, err)
	return false
}

func (d *Daemon) claimClaudeReport(seq int64) bool {
	d.claudeReportMu.Lock()
	defer d.claudeReportMu.Unlock()
	if d.claudeReportBusy[seq] {
		return false
	}
	if d.claudeReportBusy == nil {
		d.claudeReportBusy = map[int64]bool{}
	}
	d.claudeReportBusy[seq] = true
	return true
}

func (d *Daemon) releaseClaudeReport(seq int64) {
	d.claudeReportMu.Lock()
	delete(d.claudeReportBusy, seq)
	d.claudeReportMu.Unlock()
}
