package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// A worker stop the daemon refuses ends in an error line the owner reads. The
// screen reports the same text with the session, so the daemon log holds it.
func TestRefusedWorkerStopIsReportedForTheDaemonLog(t *testing.T) {
	m, src := workerModel(120, 30)
	src.actErr = errors.New("machine Mac is offline")
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	m, cmd := press(m, tea.KeyPressMsg{Text: "y", Code: []rune("y")[0]})
	m = finish(t, m, cmd)

	if !m.notice.err || m.notice.text != "Not stopped: machine Mac is offline" {
		t.Fatalf("notice = %+v", m.notice)
	}
	if len(src.screenErrors) != 1 {
		t.Fatalf("reported %d error lines, want 1: %+v", len(src.screenErrors), src.screenErrors)
	}
	if got := src.screenErrors[0]; got.text != m.notice.text || got.sessionID != workerID {
		t.Fatalf("report = %+v, want text %q session %q", got, m.notice.text, workerID)
	}
}

// The operator client already reported the failed call. The screen still shows
// the line but does not report the same failure a second time.
func TestReportedCallErrorIsNotReportedAgain(t *testing.T) {
	m, src := workerModel(120, 30)
	src.actErr = &operator.ReportedError{Err: errors.New("machine Mac is offline")}
	m = selectID(m, workerID)
	m, _ = press(m, tea.KeyPressMsg{Text: "x", Code: []rune("x")[0]})
	m, cmd := press(m, tea.KeyPressMsg{Text: "y", Code: []rune("y")[0]})
	m = finish(t, m, cmd)

	if !m.notice.err || m.notice.text != "Not stopped: machine Mac is offline" {
		t.Fatalf("notice = %+v", m.notice)
	}
	if len(src.screenErrors) != 0 {
		t.Fatalf("reported an already reported call again: %+v", src.screenErrors)
	}
}

// A TUI action that ends in an error it did not get from a call, here a
// message the screen refuses because the session is offline, sends one
// client_error report with that text through the real operator client.
func TestScreenRefusalReachesTheClientErrorReport(t *testing.T) {
	reported := make(chan protocol.ClientError, 4)
	d := startFakeDaemon(t, "", func(method string, params json.RawMessage) (any, string) {
		switch method {
		case protocol.OpClientError:
			var e protocol.ClientError
			if json.Unmarshal(params, &e) == nil {
				reported <- e
			}
			return map[string]any{"ok": true}, ""
		case "list":
			return map[string]any{"agents": []protocol.AgentView{{
				SessionID: "s_1", NodeID: "n_here", Hostname: "here", Online: false,
			}}}, ""
		}
		return normalHandler(method, params)
	})
	src := NewDaemonSource(d.path)
	defer src.Close()
	m := New(src)
	m.loc = time.UTC
	m.now = func() time.Time { return t0 }
	m.activeOnly, m.showOffline = false, true
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = refresh(m, src)
	m = selectID(m, "s_1")
	m, _ = press(m, tea.KeyPressMsg{Text: "m", Code: []rune("m")[0]})

	const want = "That session is offline: a message would wait until it comes back."
	if !m.notice.err || m.notice.text != want {
		t.Fatalf("notice = %+v", m.notice)
	}
	select {
	case got := <-reported:
		if got.Method != protocol.ScreenErrorMethod || got.Text != want || got.Screen == "" {
			t.Fatalf("report = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the error line did not reach the client_error report")
	}
}

// An error line can only be made by the one function that reports it. A
// direct notice with err true in another file would slip past the daemon log,
// so the scan fails then.
func TestErrorNoticesGoThroughTheReporter(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	noticeErr := regexp.MustCompile(`notice\{[^}]*\berr:\s*true`)
	noticeSet := regexp.MustCompile(`m\.notice\.err\s*=\s*true`)
	found := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		n := len(noticeErr.FindAll(b, -1)) + len(noticeSet.FindAll(b, -1))
		if n == 0 {
			continue
		}
		if name != "forms.go" {
			t.Errorf("%s makes an error notice of its own; use fail, failFor or failErr", name)
		}
		found += n
	}
	// forms.go holds the one making it (showFail); fail, failFor and failErr
	// all go through it.
	if found != 1 {
		t.Fatalf("%d error notices in forms.go, want 1", found)
	}
}
