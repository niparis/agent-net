//go:build linux || darwin

package daemon

import (
	"os"

	"golang.org/x/sys/unix"
)

func homeDiskFree() uint64 {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	free, _ := diskFree(home)
	return free
}

func diskFree(path string) (uint64, bool) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil || stat.Bsize <= 0 || stat.Bavail < 0 {
		return 0, false
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), true
}
