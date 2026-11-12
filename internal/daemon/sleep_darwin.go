//go:build darwin

package daemon

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// readOSSleeps reads the power log for sleep spans. pmset keeps the log on
// disk, so the daemon reads it after a wake.
func readOSSleeps(since time.Time) []protocol.NodeSleep {
	return parsePMSetLog(runSleepCommand("pmset", "-g", "log"), since)
}
