//go:build linux

package daemon

import (
	"golang.org/x/sys/unix"
	"path/filepath"
)

func renameSkillNoReplace(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}

// renameSkillAt pins both parents and retains atomic no-replace semantics.
func renameSkillAt(from, to skillPath, noReplace bool) error {
	if from.root == to.root && !noReplace {
		return from.root.Rename(from.name, to.name)
	}
	fromDir, err := from.root.Open(filepath.Dir(from.name))
	if err != nil {
		return err
	}
	defer fromDir.Close()
	toDir, err := to.root.Open(filepath.Dir(to.name))
	if err != nil {
		return err
	}
	defer toDir.Close()
	flags := uint(0)
	if noReplace {
		flags = unix.RENAME_NOREPLACE
	}
	return unix.Renameat2(int(fromDir.Fd()), filepath.Base(from.name), int(toDir.Fd()), filepath.Base(to.name), flags)
}
