//go:build !linux && !darwin

package daemon

import "errors"

func renameSkillNoReplace(from, to string) error {
	return errors.New("atomic no-replace rename is unavailable")
}
