//go:build !linux && !darwin

package daemon

import "errors"

func lockSkillExclude(path skillPath) (func(), error)      { return nil, errors.New("unsupported") }
func appendSkillExclude(path skillPath, data []byte) error { return errors.New("unsupported") }

func readSkillExclude(path skillPath) ([]byte, error) { return nil, errors.New("unsupported") }
