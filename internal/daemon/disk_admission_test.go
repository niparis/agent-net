package daemon

import (
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestAdmitDisk(t *testing.T) {
	d := &Daemon{cfg: Config{StateDir: t.TempDir(), DiskFree: func(string) (uint64, error) { return 6 << 30, nil }}}
	got := d.diskAdmission()
	if got.OK || got.Room != protocol.RoomDisk || got.Reason != "disk: 6 GB free (floor 15 GB)" {
		t.Fatalf("low disk: %+v", got)
	}
	d.cfg.DiskFree = func(string) (uint64, error) { return 16 << 30, nil }
	got = d.diskAdmission()
	if !got.OK {
		t.Fatalf("space recovered: %+v", got)
	}
}
