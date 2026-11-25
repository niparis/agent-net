package daemon

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// The migration hook runs off the start path and its note reaches the daemon
// log.
func TestMigrateClaudeWorkersLogsItsNote(t *testing.T) {
	calls := 0
	var buf bytes.Buffer
	d := &Daemon{
		log: log.New(&buf, "", 0),
		cfg: Config{MigrateClaudeWorkers: func() string {
			calls++
			return "worker window off: run agent-net setup --claude-workers-mod"
		}},
	}
	d.migrateClaudeWorkers()
	if calls != 1 {
		t.Fatalf("hook ran %d times", calls)
	}
	if !strings.Contains(buf.String(), "claude workers mod: worker window off") {
		t.Errorf("log: %q", buf.String())
	}
}
