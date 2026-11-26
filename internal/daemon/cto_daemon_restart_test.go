package daemon

import (
	"context"
	"strings"
	"testing"
)

// A Codex CTO that ran when the daemon stopped runs again after the next
// daemon start, without a message or Enter.
func TestDaemonRestartStartsTheCodexCTOAgain(t *testing.T) {
	testDaemonRestartStartsTheCTOAgain(t, "codex", "gpt-fake")
}

// The same for an OpenCode CTO.
func TestDaemonRestartStartsTheOpenCodeCTOAgain(t *testing.T) {
	testDaemonRestartStartsTheCTOAgain(t, "opencode", "fake/m1")
}

func testDaemonRestartStartsTheCTOAgain(t *testing.T, harness, model string) {
	n, logs := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, harness, model)
	ctoSendID(t, cli, "before the daemon restart")
	ctoTimelineShows(t, cli, "before the daemon restart")
	n.restart()
	eventually(t, "the "+harness+" CTO runs again after the daemon start", n.d.ctoRunning)
	if want := "cto.wake trigger=daemon_restart harness=" + harness; !strings.Contains(logs.String(), want) {
		t.Fatalf("no %q in the log:\n%s", want, logs.String())
	}
}

// A CTO the owner stopped stays stopped after a daemon restart: the new daemon
// has no start to make.
func TestDaemonRestartKeepsAnOwnerStoppedCTODown(t *testing.T) {
	n, _ := startCodexCTONode(t)
	cli := cliClient(t, n)
	setCTOHarness(t, cli, "codex", "gpt-fake")
	ctoSendID(t, cli, "before the owner stop")
	ctoTimelineShows(t, cli, "before the owner stop")
	if _, err := n.d.stopCTOSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	n.restart()
	if harness := n.d.ctoStartAfterRestart(); harness != "" {
		t.Fatalf("the daemon start plans to start the owner-stopped CTO on %s", harness)
	}
	if n.d.ctoRunning() {
		t.Fatal("the owner-stopped CTO runs after the daemon restart")
	}
}
