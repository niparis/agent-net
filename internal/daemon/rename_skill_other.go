//go:build !linux && !darwin

package daemon

import "errors"

func renameSkillNoReplace(from, to string) error {
	return errors.New("atomic no-replace rename is unavailable")
}

func renameSkillAt(from, to skillPath, noReplace bool) error {
	return errors.New("atomic rooted rename is unavailable")
}
