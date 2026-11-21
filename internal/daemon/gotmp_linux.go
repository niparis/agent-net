package daemon

import "syscall"

// tmpfsMagic is the statfs type of a tmpfs (linux/magic.h).
const tmpfsMagic = 0x01021994

// probeShm asks the kernel what /dev/shm is. A failed statfs reports no room, so
// the worker folder goes on disk.
func probeShm() shmInfo {
	var st syscall.Statfs_t
	if err := syscall.Statfs("/dev/shm", &st); err != nil {
		return shmInfo{}
	}
	return shmInfo{
		tmpfs: uint64(st.Type) == tmpfsMagic,
		free:  uint64(st.Bavail) * uint64(st.Bsize),
	}
}
