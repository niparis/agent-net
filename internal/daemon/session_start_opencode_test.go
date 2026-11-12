package daemon

import (
	"os"
	"strings"
	"testing"
)

// Every OpenCode session the daemon opens lives on a service: the owner's
// `opencode service`, or a worker's private `opencode serve`. A plan that runs
// `opencode --session` without --server would host the session in a terminal.
func TestOpenCodePlansNeverHostASessionStandalone(t *testing.T) {
	files := []string{"enter.go", "session_start_opencode.go", "cto_runtime.go"}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, `"--session"`) && !strings.Contains(line, `"--server"`) {
				t.Errorf("%s:%d runs opencode --session without --server", f, i+1)
			}
		}
	}
}
