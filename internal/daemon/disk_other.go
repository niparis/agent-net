//go:build !linux && !darwin

package daemon

func homeDiskFree() uint64 { return 0 }

func diskFree(path string) (uint64, bool) { return 0, false }

func diskPercentUsed(path string) (float64, bool) { return 0, false }
