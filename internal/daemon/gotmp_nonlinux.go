//go:build !linux

package daemon

// probeShm reports no tmpfs room. macOS has no /dev/shm, so its workers use the
// Go temp folder on disk.
func probeShm() shmInfo { return shmInfo{} }
