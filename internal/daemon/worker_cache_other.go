//go:build !linux && !darwin

package daemon

import "errors"

func processHasOpenPath(string) (bool, error) {
	return false, errors.New("cannot inspect open files on this system")
}
