package daemon

import (
	"fmt"
	"os"
	"strconv"

	"github.com/niparis/agent-net/internal/protocol"
)

func (d *Daemon) diskAdmission() Decision {
	floor := d.cfg.Workers.MinFreeGB
	if floor == 0 {
		floor = 15
	}
	if v, err := strconv.ParseUint(os.Getenv("AGENT_NET_MIN_FREE_GB"), 10, 64); err == nil && v > 0 {
		floor = v
	}
	probe := d.cfg.DiskFree
	if probe == nil {
		probe = diskFree
	}
	free, err := probe(d.cfg.StateDir)
	if err == nil && free/(1<<30) < floor {
		return Decision{Room: protocol.RoomDisk, Reason: fmt.Sprintf("disk: %d GB free (floor %d GB)", free/(1<<30), floor), Headroom: 0}
	}
	return Decision{OK: true, Room: protocol.RoomOK, Headroom: -1}
}
