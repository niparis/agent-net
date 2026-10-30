//go:build darwin

package daemon

// processEnvVar is not available on macOS without extra privileges: the
// default tmux server is then used to find a pane.
func processEnvVar(pid int, key string) string { return "" }
