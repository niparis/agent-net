package operator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The TUI tells the daemon that a session Enter failed in the terminal it
// handed over, with the command's last output, so the daemon log holds it.
func TestEnterFailureIsSentToTheDaemon(t *testing.T) {
	fake, path := startFake(t, func(string, json.RawMessage) (any, string) {
		return map[string]any{}, ""
	})
	c := New(path)
	if _, err := c.SessionEnter(context.Background(), protocol.EnterRequest{SessionID: "s_1"}); err != nil {
		t.Fatal(err)
	}
	c.ReportEnterFailure(protocol.EnterFailure{
		SessionID: "s_1", Host: "mac-1",
		Err: "exit status 1", Output: "herdr: terminal term_66 already has an attached client",
	})
	waitFor(t, "enter_failure report", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.params[protocol.OpEnterFailure] != nil
	})
	fake.mu.Lock()
	raw := fake.params[protocol.OpEnterFailure]
	fake.mu.Unlock()
	var got protocol.EnterFailure
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "s_1" || got.Host != "mac-1" || got.Err != "exit status 1" ||
		got.Output != "herdr: terminal term_66 already has an attached client" {
		t.Fatalf("report %+v", got)
	}
}
