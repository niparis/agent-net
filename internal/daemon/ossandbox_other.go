//go:build !linux

package daemon

import "errors"

// osSandboxName is the wrapper on this OS. Only Linux has one today.
const osSandboxName = "bubblewrap"

// osSandboxReady refuses: the macOS wrapper (sandbox-exec) is designed in
// docs/design/worker-os-sandbox.md, not built.
func osSandboxReady() error {
	return errors.New("the OS sandbox is built for Linux only (bubblewrap); the macOS one (sandbox-exec) is designed but not built yet")
}
