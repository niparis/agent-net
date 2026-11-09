package daemon

import (
	"log"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A TUI request that timed out on the client side writes one client_error
// log line and bumps the gateway's per-method counter. The daemon never saw
// that timeout, which is why it was invisible before.
func TestClientErrorIsLoggedAndCounted(t *testing.T) {
	logs := &ctoLogBuffer{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{logger: log.New(logs, "", 0)})
	tui := dialLocal(t, n.sock)
	tui.mustCall("hello", map[string]any{"client": "cli", "tui": true})

	tui.mustCall(protocol.OpClientError, protocol.ClientError{Method: "proposal_list", Ms: 5001, TimeoutMs: 5000, Timeout: true,
		Screen: "desk", Err: "context deadline exceeded"})

	const want = `event=client_error method=proposal_list ms=5001 timeout=5000 screen=desk err="context deadline exceeded"`
	eventually(t, "client_error line", func() bool { return strings.Count(logs.String(), want) == 1 })
	eventually(t, "gateway counter", func() bool {
		out, err := g.gw.MachineGet(t.Context(), protocol.MachineGetRequest{Machine: n.cfg.NodeID})
		if err != nil || out.Requests == nil || out.Requests.ClientErrors == nil {
			return false
		}
		for _, r := range out.Requests.ClientErrors.Since {
			if r.Method == "proposal_list" && r.Origin == protocol.ViaTUI && r.Errors == 1 && r.Timeouts == 1 {
				return true
			}
		}
		return false
	})
}

// The text of a report comes from a client that may be a bug or a stranger on
// the socket: control characters and long text must not reach the log.
func TestClientErrorTextIsCleaned(t *testing.T) {
	logs := &ctoLogBuffer{}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{logger: log.New(logs, "", 0)})
	cli := cliClient(t, n)
	cli.mustCall(protocol.OpClientError, protocol.ClientError{Method: "info\nevent=fake", Screen: "a\x1b[31mb", Err: strings.Repeat("x", 500) + "\nevent=forged"})
	eventually(t, "client_error line", func() bool { return strings.Contains(logs.String(), "event=client_error") })
	var lines []string
	for _, l := range strings.Split(logs.String(), "\n") {
		if strings.Contains(l, "event=client_error") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 || strings.Contains(logs.String(), "\x1b") || !strings.Contains(lines[0], `method="info event=fake"`) || strings.Contains(logs.String(), "\nevent=forged") {
		t.Fatalf("log line not cleaned: %q", lines)
	}
	if len(lines[0]) > 400 {
		t.Fatalf("log line not cut: %d bytes", len(lines[0]))
	}
}
