package operator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// An error line the TUI shows the owner is sent through the existing
// client_error report, with the screen, the text and the session when there is
// one. The daemon cannot see the screen, so without this report a failure the
// owner reads never reaches the log the lead uses.
func TestScreenErrorIsSentToTheDaemon(t *testing.T) {
	fake, path := startFake(t, func(string, json.RawMessage) (any, string) {
		return map[string]any{}, ""
	})
	c := New(path)
	c.SetScreen("sessions")
	c.ReportScreenError("Not stopped: machine Mac is offline", "s_1")
	waitFor(t, "screen error report", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.params[protocol.OpClientError] != nil
	})
	fake.mu.Lock()
	raw := fake.params[protocol.OpClientError]
	fake.mu.Unlock()
	var got protocol.ClientError
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Method != protocol.ScreenErrorMethod || got.Screen != "sessions" ||
		got.Text != "Not stopped: machine Mac is offline" || got.SessionID != "s_1" {
		t.Fatalf("report = %+v", got)
	}
}

// A call failure is reported by the client itself. Its error is marked so the
// screen that shows it does not report the same failure a second time.
func TestFailedCallIsMarkedAsReported(t *testing.T) {
	fake, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		if method == "refused" {
			return nil, "no way"
		}
		return map[string]any{}, ""
	})
	c := New(path)
	_, err := c.Call(context.Background(), "refused", nil)
	if err == nil {
		t.Fatal("expected the refusal")
	}
	if !IsReported(err) || err.Error() != "no way" {
		t.Fatalf("error = %v reported=%v", err, IsReported(err))
	}
	waitFor(t, "refusal report", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.params[protocol.OpClientError] != nil
	})
}
