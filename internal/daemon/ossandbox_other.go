//go:build !linux && !darwin

package daemon

import "errors"

// OSSandboxDefaultOn tells whether Pi and OpenCode workers run in the OS
// sandbox without being asked. There is no wrapper on this OS, so it is off;
// Linux and macOS have it on (decision 0171, phase 4b).
const OSSandboxDefaultOn = false

// osSandboxName is the wrapper on this OS. There is none here.
const osSandboxName = "none"

var errNoOSSandbox = errors.New("the OS sandbox is built for Linux (bubblewrap) and macOS (sandbox-exec) only")

func osSandboxReady() error { return errNoOSSandbox }

func osSandboxTool() (string, error) { return "", errNoOSSandbox }

func osSandboxCommand(p osSandboxPolicy, bin string, args []string) []string {
	return append([]string{bin}, args...)
}

func osSandboxTmpEnv(tmp string) string { return tmp }
