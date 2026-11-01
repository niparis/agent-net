//go:build linux || darwin

package daemon

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func homeDiskFree() uint64 {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(home, &stat); err != nil || stat.Bsize <= 0 || stat.Bavail < 0 {
		return 0
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize)
}

func diskHardlinked(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return !ok || s.Nlink > 1
}
