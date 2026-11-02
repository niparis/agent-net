//go:build !linux && !darwin

package daemon

func homeDiskFree() uint64 { return 0 }
