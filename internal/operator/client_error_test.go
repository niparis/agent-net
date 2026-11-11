package operator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A call that ends by its deadline is reported to the daemon, with the method,
// the time waited, the deadline and the screen that asked. The report is what
// puts a TUI timeout in the daemon log.
func TestTimedOutCallIsReportedToTheDaemon(t *testing.T) {
	fake, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		if method == "proposal_list" {
			time.Sleep(time.Hour) // never answers
		}
		return map[string]any{}, ""
	})
	c := New(path)
	c.SetScreen("desk")
	ctx, cancel := WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := c.Call(ctx, "proposal_list", nil); err == nil {
		t.Fatal("expected a timeout")
	}
	waitFor(t, "client_error report", func() bool {
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
	if got.Method != "proposal_list" || got.Screen != "desk" || !got.Timeout || got.TimeoutMs != 80 || got.Ms < 70 || got.Ms > 1500 ||
		!strings.Contains(got.Err, "deadline exceeded") {
		t.Fatalf("report = %+v", got)
	}
}

// A refusal from the daemon is a failed request too, and reported. A call the
// user cancelled, and a report about a report, are not.
func TestOnlyRealFailuresAreReported(t *testing.T) {
	fake, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		switch method {
		case "refused":
			return nil, "no way"
		case "slow":
			time.Sleep(time.Hour)
		case protocol.OpClientError:
			return nil, "report refused" // must not start a loop of reports
		}
		return map[string]any{}, ""
	})
	c := New(path)
	if _, err := c.Call(context.Background(), "fine", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), "refused", nil); err == nil {
		t.Fatal("expected a refusal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	if _, err := c.Call(ctx, "slow", nil); err == nil {
		t.Fatal("expected a cancel")
	}
	waitFor(t, "refusal report", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.params[protocol.OpClientError] != nil
	})
	time.Sleep(150 * time.Millisecond) // a wrong extra report would arrive by now
	fake.mu.Lock()
	defer fake.mu.Unlock()
	n := 0
	for _, m := range fake.calls {
		if m == protocol.OpClientError {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d reports, want 1 (calls %v)", n, fake.calls)
	}
	var got protocol.ClientError
	_ = json.Unmarshal(fake.params[protocol.OpClientError], &got)
	if got.Method != "refused" || got.Timeout || got.Err != "no way" {
		t.Fatalf("report = %+v", got)
	}
}

// The web console labels its reports so they are told apart from the TUI's.
func TestPhoneClientReportsTheWebScreen(t *testing.T) {
	fake, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		if method == "nodes" {
			return nil, "boom"
		}
		return map[string]any{}, ""
	})
	c := NewPhone(path)
	_, _ = c.Call(context.Background(), "nodes", nil)
	waitFor(t, "report", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.params[protocol.OpClientError] != nil
	})
	var got protocol.ClientError
	fake.mu.Lock()
	_ = json.Unmarshal(fake.params[protocol.OpClientError], &got)
	fake.mu.Unlock()
	if got.Screen != "web" {
		t.Fatalf("screen = %q, want web", got.Screen)
	}
}

// An answer that says the daemon or the gateway does not know the call is
// version skew between an older peer and a newer client. It is not a slow or
// failed request, so it is not reported: best-effort reads of a newer screen
// get this answer from an older daemon on every refresh.
func TestOldPeerAnswersAreNotReported(t *testing.T) {
	for _, answer := range []string{`unknown method "scores_get"`, "unknown frame type query.x", `method "chief_get" is not available to cli clients`} {
		fake, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
			if method == "newer_read" {
				return nil, answer
			}
			return map[string]any{}, ""
		})
		c := New(path)
		if _, err := c.Call(context.Background(), "newer_read", nil); err == nil {
			t.Fatalf("%q: expected the refusal", answer)
		}
		// A report would reach the fake within a few milliseconds.
		time.Sleep(100 * time.Millisecond)
		fake.mu.Lock()
		calls := append([]string(nil), fake.calls...)
		fake.mu.Unlock()
		for _, m := range calls {
			if m == protocol.OpClientError {
				t.Fatalf("%q: an old-peer answer was reported (calls %v)", answer, calls)
			}
		}
	}
}

// A timed-out call reports the configured timeout of the call, even when time
// elapsed before the call began.
func TestConfiguredTimeoutIsReportedWhenTimeElapsed(t *testing.T) {
	fake, path := startFake(t, func(method string, _ json.RawMessage) (any, string) {
		if method == "slow" {
			time.Sleep(time.Hour)
		}
		return map[string]any{}, ""
	})
	c := New(path)
	ctx, cancel := WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond)
	if _, err := c.Call(ctx, "slow", nil); err == nil {
		t.Fatal("expected a timeout")
	}
	waitFor(t, "client_error report", func() bool {
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
	if got.TimeoutMs != 80 {
		t.Fatalf("got TimeoutMs = %d, want 80", got.TimeoutMs)
	}
}
