//go:build !darwin && !linux

package daemon

import (
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// readOSSleeps has no OS log to read on this platform. The clock jump still
// reports every sleep.
func readOSSleeps(since time.Time) []protocol.NodeSleep {
	return nil
}
