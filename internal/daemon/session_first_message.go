package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// startFirstMessage is the first message of a session start and who wrote it.
// When it goes as an agent-net message, only the owner's own words go out as
// the owner. Text the CTO wrote goes out from the CTO's session, so the agent
// reads it with the label of a message from the CTO.
type startFirstMessage struct {
	Text string
	// ByOwner says the owner wrote Text in the TUI or command line.
	ByOwner bool
	// From is the session that wrote Text when the owner did not. Empty means
	// agent-net does not know, as for a start relayed by an older machine.
	From string
}

func firstMessageOf(p protocol.SessionStartRequest) startFirstMessage {
	return startFirstMessage{Text: p.FirstMessage, ByOwner: p.FirstMessageByOwner, From: p.FirstMessageFrom}
}

// firstMessageIsPrompt says the start passes its first message to the harness
// as the first prompt, not as an agent-net message: a Claude start without an
// agent identity (on the command line, marked when the owner did not write
// it), an OpenCode start (through the service's prompt API) and an agy start
// (as its -i prompt). A relayed start keeps such a message with the request, so
// the machine that runs the start passes it on.
func firstMessageIsPrompt(p protocol.SessionStartRequest) bool {
	return p.Harness == "opencode" || p.Harness == "agy" || ((p.Harness == "claude" || p.Harness == "") && p.AgentID == "")
}

// firstMessageSender is the session the first message goes out from: the
// owner for the owner's words, otherwise the session of this machine that
// wrote it. There is no fallback to the owner.
func (d *Daemon) firstMessageSender(m startFirstMessage) (string, error) {
	if m.ByOwner {
		if owner := d.ownerSessionID(); owner != "" {
			return owner, nil
		}
		return "", errors.New("the owner session is not registered yet")
	}
	if m.From == "" {
		return "", errors.New("agent-net does not know who wrote it, and only the owner's own words go out as the owner")
	}
	// The gateway takes a message only from a session of the machine that
	// sends it. The check comes first so the message never waits in the outbox
	// for a refusal.
	if m.From != d.ctoSessionID() && d.sessionByID(m.From) == nil {
		return "", fmt.Errorf("its writer %s has no session on this machine", m.From)
	}
	return m.From, nil
}

// sendFirstMessage sends the first message to a started session from the
// session that wrote it. An empty message sends nothing.
func (d *Daemon) sendFirstMessage(ctx context.Context, to string, m startFirstMessage) error {
	if m.Text == "" {
		return nil
	}
	sender, err := d.firstMessageSender(m)
	if err == nil {
		_, err = d.send(ctx, protocol.MsgSend{Sender: sender, Recipient: to, Type: protocol.TypeRequest, Body: m.Text})
	}
	if err != nil {
		return fmt.Errorf("the first message was not sent to %s: %w", to, err)
	}
	return nil
}

// keepUnsentFirstMessage keeps the text of a first message that was not sent
// in the start record, and makes its writer the one told of the failure.
func keepUnsentFirstMessage(r *protocol.StartedSession, m startFirstMessage) {
	r.UnsentFirstMessage = m.Text
	r.Requester = firstNonEmpty(r.Requester, m.From)
}

// sendRelayedFirstMessage sends the first message of a start that another
// machine runs, once that machine has registered the session. That machine
// cannot send as a session of this one, so the message stays here with its
// writer, as a worker's task does (decision 0018).
func (d *Daemon) sendRelayedFirstMessage(id, node string, m startFirstMessage) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		// The other machine gives up on the start after its own start timeout;
		// the second one covers the relay and the polls around it.
		ctx, cancel := context.WithTimeout(d.ctoCtx, 2*d.workerSettings("").StartTimeout)
		defer cancel()
		ticker := time.NewTicker(150 * time.Millisecond)
		defer ticker.Stop()
		var r protocol.StartedSession
		var err error
		for {
			r, err = d.relayedStartGet(ctx, id, node)
			if err == nil && r.Status != "starting" {
				break
			}
			select {
			case <-ctx.Done():
				err = fmt.Errorf("the session did not start in time: %w", ctx.Err())
			case <-ticker.C:
				continue
			}
			break
		}
		switch {
		case err != nil:
			err = fmt.Errorf("the first message was not sent: %w", err)
		case r.Status != "started":
			// A failed start is recorded and told by the machine that ran it.
			return
		default:
			if err = d.sendFirstMessage(ctx, r.SessionID, m); err != nil {
				err = fmt.Errorf("the session started, but %w", err)
			}
		}
		if err != nil {
			// The writer still has its text; the notice tells it to send again.
			d.Logf("session start %s: %v", id, err)
			r.ID, r.Error = id, err.Error()
			d.notifyRequesterStartFailed(m.From, r)
		}
	}()
}
