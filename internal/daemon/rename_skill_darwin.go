//go:build darwin

package daemon

import "golang.org/x/sys/unix"

func renameSkillNoReplace(from, to string) error {
	return unix.RenamexNp(from, to, unix.RENAME_EXCL)
}
