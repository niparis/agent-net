package daemon

import (
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const openCodeExe = "/opt/x/node_modules/@opencode/cli/bin/opencode.exe"

// pluginConfirmNode is a node whose OpenCode sessions have the delivery check on,
// with a short wait.
func pluginConfirmNode(t *testing.T) (*node, *logBuf) {
	t.Helper()
	g := startGateway(t, t.TempDir(), "")
	logs := &logBuf{}
	n := startNode(t, g, nodeOpts{exe: openCodeExe, logger: log.New(logs, "", 0), cfg: func(c *Config) {
		c.testNoDeliveryConfirm = false
		c.deliveryConfirmWait = scaled(300 * time.Millisecond)
	}})
	return n, logs
}

// pluginPair is a sender and a target OpenCode session on one node.
func pluginPair(t *testing.T, n *node) (sender, target *localClient, targetID string) {
	t.Helper()
	sender, _ = helloOpenCode(t, n, "ses_sender", "/work/sender")
	target, targetID = helloOpenCode(t, n, "ses_target", "/work/target")
	return sender, target, targetID
}

func sendTo(t *testing.T, from *localClient, to, body string) protocol.Message {
	t.Helper()
	return sendResult(t, from.mustCall("send", map[string]any{"to": to, "body": body}))
}

// A turn that fails after the message was queued is not a delivery. The cause
// stays on the session, it sets no usage limit, and the sender is told once.
func TestFailedOpenCodeTurnTellsTheSenderAndKeepsTheCause(t *testing.T) {
	t.Parallel()
	n, _ := pluginConfirmNode(t)
	sender, target, targetID := pluginPair(t, n)
	sent := sendTo(t, sender, targetID, "please run the check")
	if got := target.nextPush(); got.ID != sent.ID {
		t.Fatalf("target got %s, want %s", got.ID, sent.ID)
	}

	cause := "AI.Error: misalignment_policy_violation: This request was blocked by our safety systems. Reason: Potentially unintended activity. api_key=sk-abcdefghijklmnopqrstuvwx"
	target.mustCall("status", map[string]any{"status": "busy"})
	target.mustCall("turn_error", map[string]any{"text": cause})
	target.mustCall("status", map[string]any{"status": "idle"})

	notice := sender.nextPush()
	want := "was not delivered: the session's turn failed: AI.Error: misalignment_policy_violation: This request was blocked by our safety systems."
	if !strings.Contains(notice.Body, want) || notice.ParentID != sent.ID {
		t.Fatalf("notice = %+v, want it to contain %q", notice, want)
	}
	if strings.Contains(notice.Body, "sk-abcdefghij") {
		t.Fatalf("the secret reached the notice: %s", notice.Body)
	}

	a := limitOf(t, n, targetID)
	if a.LimitedUntil != 0 {
		t.Fatalf("a refusal is not a usage limit: %+v", a)
	}
	if !strings.HasPrefix(a.TurnError, "AI.Error: misalignment_policy_violation") || strings.Contains(a.TurnError, "sk-abcdefghij") || a.TurnErrorAt == 0 {
		t.Fatalf("turn error = %q at %d", a.TurnError, a.TurnErrorAt)
	}

	// The same failure again tells nobody a second time. The gateway may push
	// the one notice more than once; a different notice is the fault.
	target.mustCall("turn_error", map[string]any{"text": cause})
	deadline := time.After(scaled(300 * time.Millisecond))
	for done := false; !done; {
		select {
		case again := <-sender.Pushes:
			if again.ID != notice.ID {
				t.Fatalf("second notice %s: %s", again.ID, again.Body)
			}
		case <-deadline:
			done = true
		}
	}

	// A turn that succeeds clears the failure.
	target.mustCall("status", map[string]any{"status": "busy"})
	target.mustCall("status", map[string]any{"status": "idle"})
	eventually(t, "a good turn clears the failure", func() bool { return limitOf(t, n, targetID).TurnError == "" })
}

// A real usage limit keeps the limit path, and the failure shows with it.
func TestOpenCodeUsageLimitStillLimitsTheSession(t *testing.T) {
	t.Parallel()
	n, _ := pluginConfirmNode(t)
	_, target, targetID := pluginPair(t, n)
	target.mustCall("turn_error", map[string]any{"text": piLimitText})
	eventually(t, "limit set", func() bool { return limitOf(t, n, targetID).LimitedUntil != 0 })
	if got := limitOf(t, n, targetID).TurnError; !strings.Contains(got, "usage limit") {
		t.Fatalf("turn error = %q", got)
	}
}

// A message the plugin queued but no turn started for is delivered once more,
// and then the sender is told it was not delivered.
func TestOpenCodeMessageWithNoTurnIsRedeliveredThenReported(t *testing.T) {
	t.Parallel()
	n, logs := pluginConfirmNode(t)
	sender, target, targetID := pluginPair(t, n)
	sent := sendTo(t, sender, targetID, "anyone there?")
	if got := target.nextPush(); got.ID != sent.ID {
		t.Fatalf("first delivery %s", got.ID)
	}
	if got := target.nextPushOrLog("the second delivery", logs); got.ID != sent.ID {
		t.Fatalf("redelivery %s", got.ID)
	}
	notice := sender.nextPushOrLog("the not delivered notice", logs)
	if !strings.Contains(notice.Body, "was not delivered: the session started no turn for it") {
		t.Fatalf("notice = %s", notice.Body)
	}
	select {
	case third := <-target.Pushes:
		t.Fatalf("a third delivery: %s", third.ID)
	default:
	}
}

// A session that is busy at the check is not delivered to again: its harness
// takes the message when the turn ends. A turn that starts ends the check. The
// gateway holds a message for a busy session, so the test hands the message to
// the daemon directly.
func TestOpenCodeBusySessionIsNotRedelivered(t *testing.T) {
	t.Parallel()
	n, logs := pluginConfirmNode(t)
	sender, target, targetID := pluginPair(t, n)
	target.mustCall("status", map[string]any{"status": "busy"})
	m := protocol.Message{ID: "m_busy_check", ConversationID: "c_busy_check", From: "s_other", To: targetID, Type: protocol.TypeNotification, Body: "when you can"}
	if res := n.d.deliverTo(context.Background(), n.d.sessionByID(targetID), m); !res.Delivered {
		t.Fatalf("delivery refused: %s", res.Detail)
	}
	if got := target.nextPush(); got.ID != m.ID {
		t.Fatalf("delivery %s", got.ID)
	}
	// Three checks pass while the session stays busy.
	time.Sleep(scaled(1000 * time.Millisecond))
	select {
	case again := <-target.Pushes:
		t.Fatalf("redelivered to a busy session: %s", again.ID)
	case note := <-sender.Pushes:
		t.Fatalf("notice for a busy session: %s", note.Body)
	default:
	}
	if strings.Contains(logs.String(), "session.delivery_retry") {
		t.Fatalf("retry logged:\n%s", logs.String())
	}
	if n.d.checkRuns.Load() != 1 {
		t.Fatalf("the check of the held message is not running: %d", n.d.checkRuns.Load())
	}
	// Its next turn starts: the check ends as confirmed.
	target.mustCall("status", map[string]any{"status": "idle"})
	target.mustCall("status", map[string]any{"status": "busy"})
	eventually(t, "the turn confirms the delivery", func() bool {
		return strings.Contains(logs.String(), "session.delivery_confirmed msg="+m.ID)
	})
}
