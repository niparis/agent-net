package daemon

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

type diskPressure struct {
	path        string
	free, floor uint64
}

func (d *Daemon) lowDisk() (diskPressure, bool) {
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
	paths := append([]string{d.cfg.StateDir}, d.cfg.WorkerCacheDirs...)
	paths = append(paths, d.cfg.WorkerTempDirs...)
	for _, path := range paths {
		free, err := probe(path)
		if err == nil && free/(1<<30) < floor {
			return diskPressure{path, free, floor}, true
		}
	}
	return diskPressure{}, false
}

func (d *Daemon) diskAdmission() Decision {
	if p, low := d.lowDisk(); low {
		select {
		case d.cacheWake <- struct{}{}:
		default:
		}
		return Decision{Room: protocol.RoomDisk, Reason: fmt.Sprintf("disk: %d GB free (floor %d GB)", p.free/(1<<30), p.floor), Headroom: 0}
	}
	return Decision{OK: true, Room: protocol.RoomOK, Headroom: -1}
}

func (d *Daemon) sendDiskNotice(ctx context.Context, p diskPressure) error {
	root := filesystemRoot
	if d.cfg.DiskRoot != nil {
		root = d.cfg.DiskRoot
	}
	var b strings.Builder
	fmt.Fprintf(&b, "disk: %d GB free (floor %d GB) on %s", p.free/(1<<30), p.floor, p.path)
	for _, f := range largestDiskFoldersContext(ctx, root(p.path)) {
		fmt.Fprintf(&b, "\n%s: %s", f.path, fmtBytes(float64(f.size)))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.cfg.DiskNotice != nil {
		return d.cfg.DiskNotice(ctx, b.String())
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := d.call(callCtx, "worker.disk_notice", map[string]string{"text": b.String()})
	return err
}
