package daemon

// Go writes build and test binaries to its temp folder (GOTMPDIR) and deletes
// them after each run. On a disk those writes wear an SSD for nothing, so on
// Linux a tmpfs in RAM takes them when it has room.

const (
	// gotmpDiskDir is the Go temp folder on disk: the default when no RAM folder
	// has room, and on systems without /dev/shm.
	gotmpDiskDir = "/tmp/agent-net-gotmp"
	// gotmpShmDir is the Go temp folder on /dev/shm, a tmpfs on Linux.
	gotmpShmDir = "/dev/shm/agent-net-gotmp"
	// gotmpShmMinFree is the free room /dev/shm needs before Go temp files go
	// there. One package test binary is tens of megabytes, so this leaves room
	// for several runs at once.
	gotmpShmMinFree = 4 << 30
)

// shmInfo is what the daemon needs to know about /dev/shm.
type shmInfo struct {
	tmpfs bool   // /dev/shm is a tmpfs, so its files live in RAM
	free  uint64 // bytes a user other than root can still write
}

// defaultGoTmpDir is the Go temp folder when the owner names none: the RAM folder
// when /dev/shm is a tmpfs with at least gotmpShmMinFree free, else the disk folder.
func defaultGoTmpDir(shm shmInfo) string {
	if shm.tmpfs && shm.free >= gotmpShmMinFree {
		return gotmpShmDir
	}
	return gotmpDiskDir
}

// shmNow is the state of /dev/shm at the moment of the call. A worker start
// asks again, so a tmpfs that fills up moves the next worker to the disk folder.
func (d *Daemon) shmNow() shmInfo {
	if d.cfg.testShm != nil {
		return d.cfg.testShm()
	}
	return probeShm()
}
