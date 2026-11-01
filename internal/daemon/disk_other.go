//go:build !linux && !darwin

package daemon

import "fmt"

func homeDiskFree() uint64 { return 0 }

func diskFree(path string) (uint64, error) { return 0, fmt.Errorf("disk space unavailable") }
