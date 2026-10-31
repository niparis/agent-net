//go:build !linux && !darwin

package daemon

import "errors"

func lockSkillExclude(path string) (func(), error)      { return nil, errors.New("unsupported") }
func appendSkillExclude(path string, data []byte) error { return errors.New("unsupported") }

func readSkillExclude(path string) ([]byte, error) { return nil, errors.New("unsupported") }
