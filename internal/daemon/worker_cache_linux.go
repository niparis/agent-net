//go:build linux

package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// processHasOpenPath is conservative: if a process cannot be inspected it
// returns an error, so the caller keeps the entry.
func processHasOpenPath(path string) (bool, error) {
	return processHasOpenPathIn("/proc", path)
}

func processHasOpenPathIn(proc, path string) (bool, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	pids, err := os.ReadDir(proc)
	if err != nil {
		return false, err
	}
	var unknown error
	for _, p := range pids {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		for _, name := range []string{"cwd", "root", "exe"} {
			target, err := os.Readlink(filepath.Join(proc, p.Name(), name))
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					unknown = err
				}
				continue
			}
			if target == path || target == path+" (deleted)" || strings.HasPrefix(target, path+string(os.PathSeparator)) {
				return true, nil
			}
		}
		dir := filepath.Join(proc, p.Name(), "fd")
		fds, err := os.ReadDir(dir)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				unknown = err
			}
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(dir, fd.Name()))
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					unknown = err
				}
				continue
			}
			if target == path || target == path+" (deleted)" || strings.HasPrefix(target, path+string(os.PathSeparator)) {
				return true, nil
			}
		}
	}
	return false, unknown
}
