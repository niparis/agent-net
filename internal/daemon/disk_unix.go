//go:build linux || darwin

package daemon

import (
	"errors"
	"os"
	"path/filepath"
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

func diskFree(path string) (uint64, error) {
	for {
		var stat unix.Statfs_t
		err := unix.Statfs(path, &stat)
		if err == nil {
			return uint64(stat.Bavail) * uint64(stat.Bsize), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return 0, err
		}
		path = parent
	}
}

func diskDevice(path string) (uint64, bool) {
	var s unix.Stat_t
	if err := unix.Stat(path, &s); err != nil {
		return 0, false
	}
	return uint64(s.Dev), true
}

func diskHardlinked(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return !ok || s.Nlink > 1
}
