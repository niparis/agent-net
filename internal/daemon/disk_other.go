//go:build !linux && !darwin

package daemon

import (
	"os"
)

func homeDiskFree() uint64 { return 0 }

func diskHardlinked(os.FileInfo) bool { return true }
