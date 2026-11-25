package daemon

import (
	"context"
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
// agent-net does not fake a matching mode, set crossSessionInbound, or send a
// held message again. The owner decides those.

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
	r, ok, handled, err := d.st.markClaudeHeld(recipient, e)
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
	detail := d.claudeHeldDetail()
	d.Logf("session.delivery_held msg=%s sender=%s recipient=%s detail=%q", r.MessageID, r.Sender, recipient, detail)
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := d.call(callCtx, "delivery.held", protocol.DeliveryUpdate{
		MessageID: r.MessageID, State: protocol.StateHeldBySession, Detail: detail}); err != nil {
		d.Logf("delivery.held %s: %v", r.MessageID, err)
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
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := d.call(callCtx, "delivery.released", protocol.DeliveryUpdate{
			MessageID: id, State: protocol.StateDelivered, Detail: "released in Claude Code"})
		cancel()
		if err != nil {
			// The gateway keeps it held. The transcript entry is applied once,
			// so a release that cannot reach the gateway stays held there.
			d.Logf("delivery.released %s: %v", id, err)
			continue
		}
		d.Logf("session.delivery_released msg=%s", id)
	}
}
