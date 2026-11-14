package daemon

import (
	"log"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// A TUI error line reaches the daemon log through the client_error report:
// method=screen, the screen, the session and the text the owner read. This is
// the line the lead reads with agent_machine_log.
func TestTUIErrorIsLoggedThroughClientError(t *testing.T) {
	logs := &ctoLogBuffer{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{logger: log.New(logs, "", 0)})

	src := operator.New(n.sock)
	src.SetScreen("sessions")
	src.ReportScreenError("Not stopped: machine Mac is offline", "s_1")

	const want = `event=client_error method=screen ms=0 timeout=0 screen=sessions err="" session=s_1 text="Not stopped: machine Mac is offline"`
	eventually(t, "client_error line", func() bool { return strings.Count(logs.String(), want) == 1 })
}

// Screen text comes from the screen and can hold control characters or long
// text. It is cleaned and bounded like decision 0167's enter_failed output, so
// it cannot start a log line of its own.
func TestScreenErrorTextIsCleanedAndBounded(t *testing.T) {
	logs := &ctoLogBuffer{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{logger: log.New(logs, "", 0)})

	src := operator.New(n.sock)
	src.SetScreen("sessions")
	src.ReportScreenError(strings.Repeat("x", protocol.ScreenErrorMaxText)+"\x1b[31m\nevent=forged", "s_1")

	eventually(t, "client_error line", func() bool {
		return strings.Contains(logs.String(), "event=client_error")
	})
	if strings.Contains(logs.String(), "\x1b") {
		t.Fatalf("a control character reached the log: %q", logs.String())
	}
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.HasPrefix(line, "event=forged") {
			t.Fatalf("the screen text started a log line of its own: %q", logs.String())
		}
	}
	var textLine string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "event=client_error") {
			textLine = line
		}
	}
	if len(textLine) > protocol.ScreenErrorMaxText+200 {
		t.Fatalf("the line was not cut to the bound: %d bytes", len(textLine))
	}
}
