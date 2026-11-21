// Package testsock lets a test that needs a unix socket skip itself in a
// sandbox that blocks unix sockets. It is imported by tests only.
package testsock

import (
	"os"
	"testing"
)

// LogEnv names a file that gets one line per skipped test. The script counts
// the lines, so a skip is never silent. It is optional.
const LogEnv = "AGENT_NET_NO_UNIX_SOCKETS_LOG"

// Env is set to 1 by scripts/test-changed.sh when its probe found that this
// sandbox cannot create a unix socket.
const Env = "AGENT_NET_NO_UNIX_SOCKETS"

// Reason is the skip message, and the text the script counts.
const Reason = "unix sockets are blocked in this sandbox; the merge queue CI runs this test (decision 0164)"

// Skip skips the test when Env is 1. Without Env it does nothing, so a real
// socket failure on a normal machine still fails the test.
func Skip(t testing.TB) {
	t.Helper()
	if os.Getenv(Env) != "1" {
		return
	}
	if path := os.Getenv(LogEnv); path != "" {
		// One short append per skip is atomic, so parallel tests and several
		// test binaries can share the file. A log error must not fail the test.
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			f.WriteString(t.Name() + "\n")
			f.Close()
		}
	}
	t.Skip(Reason)
}
