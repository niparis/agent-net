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
		"agent-net did not send it again."
}

// ClaudeHeld records a hold that the session's transcript names.
func (d *Daemon) ClaudeHeld(ctx context.Context, recipient, sender, conversation string, at time.Time) {
	r, ok, err := d.st.markClaudeHeld(recipient, sender, conversation, at)
	if err != nil {
		d.Logf("claude: cannot record the held message from %s in %s: %v", sender, conversation, err)
		return
	}
	if !ok {
		// The delivery record may be gone (old message, or a daemon restart
		// after the record was pruned). The hold is still worth a log line.
		d.Logf("claude: a held message from %s in %s has no unheld delivery record", sender, conversation)
		return
	}
	detail := d.claudeHeldDetail()
	d.Logf("session.delivery_held msg=%s sender=%s recipient=%s detail=%q", r.MessageID, sender, recipient, detail)
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := d.call(callCtx, "delivery.held", protocol.DeliveryUpdate{
		MessageID: r.MessageID, State: protocol.StateHeldBySession, Detail: detail}); err != nil {
		d.Logf("delivery.held %s: %v", r.MessageID, err)
	}
}

// ClaudeHeldReleased records that the session released a held message. The
// gateway moves it from held_by_session back to delivered.
func (d *Daemon) ClaudeHeldReleased(ctx context.Context, recipient, messageID, sender, conversation string) {
	ids, err := d.st.releaseClaudeHeld(recipient, messageID, sender, conversation, time.Now())
	if err != nil {
		d.Logf("claude: cannot record a released message from %s: %v", sender, err)
		return
	}
	for _, id := range ids {
		d.Logf("session.delivery_released msg=%s", id)
		d.sendUpdate(ctx, id, protocol.StateDelivered, "released in Claude Code")
	}
}
