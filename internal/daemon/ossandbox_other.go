//go:build !linux && !darwin

package daemon

import "errors"

// OSSandboxDefaultOn tells whether Pi and OpenCode workers run in the OS
// sandbox without being asked. On macOS it is off until the profile has been
// run on a Mac (decision 0171, phase 4b): a project opts in with
// workers.os_sandbox in node.json.
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
