//go:build !linux && !darwin

package daemon

import "errors"

func openSkillRoot(home, rel string) (string, func(), error) {
	return "", nil, errors.New("safe skill root unavailable")
}
