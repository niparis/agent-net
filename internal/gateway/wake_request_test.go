package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A bot sends a message on an HTTP request. The request ends when its handler
// returns, while the machine is still answering the wake that the message
// started. The wake must not die with the request, and the machine's answer
// must not be recorded as a failure.
func TestWakeFromBotMessageOutlivesTheRequest(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeBackground, protocol.AutonomyOff)
	app, err := p.s.CreateApp("trade", "bot", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := p.s.touchBot(app)
	if err != nil {
		t.Fatal(err)
	}
	release := p.holdWakes()
	defer release()
	request, endRequest := context.WithCancel(context.Background())
	defer endRequest()
	if _, err := p.g.botToolCall(request, bot, "agent_message_send", map[string]any{"to": id, "body": "status?"}); err != nil {
		t.Fatal(err)
	}
	p.expectWake(t, id)
	// The machine has the wake and holds its answer. The HTTP request ends now.
	endRequest()
	release()
	eventually(t, "wake over", func() bool { return !p.g.wakeInFlight(id) })
	if n := p.failures(id); n != 0 {
		t.Fatalf("wake failures = %d, want 0: the request ending cancelled the wake", n)
	}
}

// A wake that ends by cancellation or by deadline got no answer from the
// machine. Only a real answer is a refusal that is recorded.
func TestWakeFailedRecordsAnswerNotCancellation(t *testing.T) {
	p := newWakeProbe(t)
	id := p.offline(t, "claude", protocol.ModeBackground, protocol.AutonomyOff)
	for _, err := range []error{
		context.Canceled,
		fmt.Errorf("wake call: %w", context.Canceled),
		context.DeadlineExceeded,
	} {
		p.g.wakeFailed(context.Background(), id, "attempt", err)
	}
	if n := p.failures(id); n != 0 {
		t.Fatalf("failures after cancellation = %d, want 0", n)
	}
	p.g.wakeFailed(context.Background(), id, "attempt", errors.New("context canceled"))
	if n := p.failures(id); n != 1 {
		t.Fatalf("failures after a machine answer = %d, want 1", n)
	}
}
