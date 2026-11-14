package daemon

import (
	"log"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session Enter whose command failed in the TUI is written to the daemon
// log with the command's last output. The daemon never sees that command, and
// without the report the reason only existed on the owner's screen.
func TestEnterFailureIsLoggedWithTheCommandOutput(t *testing.T) {
	logs := &ctoLogBuffer{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{logger: log.New(logs, "", 0)})
	cli := cliClient(t, n)

	cli.mustCall(protocol.OpEnterFailure, protocol.EnterFailure{
		SessionID: "s_stale",
		Host:      "mac-1",
		Err:       "exit status 1",
		Output: "herdr: server shut down: terminal attach failed: terminal term_6612 " +
			"already has an attached client\n\x1b[31mretry with --takeover\nevent=forged",
	})

	const want = `event=enter_failed session=s_stale err="exit status 1" host=mac-1 output="herdr: server shut down: terminal attach failed: terminal term_6612 already has an attached client [31mretry with --takeover event=forged"`
	eventually(t, "enter_failed line", func() bool { return strings.Contains(logs.String(), want) })
	if strings.Contains(logs.String(), "\x1b") {
		t.Fatalf("a control character reached the log: %q", logs.String())
	}
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.HasPrefix(line, "event=forged") {
			t.Fatalf("the output started a log line of its own: %q", logs.String())
		}
	}
}

// A session_enter that the daemon itself refuses is in the log too, so the
// lead sees the failures that never reach a command.
func TestFailedSessionEnterIsLogged(t *testing.T) {
	logs := &ctoLogBuffer{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{logger: log.New(logs, "", 0)})

	if _, err := cliClient(t, n).call("session_enter", protocol.EnterRequest{SessionID: "s_nope"}); err == nil {
		t.Fatal("an unknown session was planned")
	}
	eventually(t, "enter_failed line", func() bool {
		return strings.Contains(logs.String(), `event=enter_failed session=s_nope err="`)
	})
}
