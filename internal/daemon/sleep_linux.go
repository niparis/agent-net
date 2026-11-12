//go:build linux

package daemon

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// readOSSleeps reads the kernel log for suspend entry and exit. kernel is
// readable by a user, so the daemon needs no privilege. A machine without
// systemd or journalctl returns nothing and the clock estimate stands alone.
func readOSSleeps(since time.Time) []protocol.NodeSleep {
	return parseJournalSleep(runSleepCommand("journalctl", "--no-pager", "-o", "short-iso",
		"--since", sleepJournalSeconds(since), "-k"), since)
}
