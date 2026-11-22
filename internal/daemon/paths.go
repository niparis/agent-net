package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ConfigDir holds node.json and ca.pem.
func ConfigDir() string {
	if d := os.Getenv("AGENT_NET_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "agent-net")
	}
	return filepath.Join(home(), ".config", "agent-net")
}

// StateDir holds the daemon's local database.
func StateDir() string {
	if d := os.Getenv("AGENT_NET_STATE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "agent-net", "daemon")
	}
	return filepath.Join(home(), ".local", "state", "agent-net", "daemon")
}

// SocketPath is the daemon's local socket.
func SocketPath() string {
	if p := os.Getenv("AGENT_NET_SOCKET"); p != "" {
		return p
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "agent-net", "daemon.sock")
	}
	return filepath.Join(fmt.Sprintf("/tmp/agent-net-%d", os.Getuid()), "daemon.sock")
}

// socketDir is the folder that holds the daemon socket and nothing else. The
// OS sandbox binds this folder, not the socket file: a file bind keeps the
// inode of the socket, and the daemon creates a new one at every restart, so a
// worker would reach a dead socket after the restart. A folder bind follows
// the new socket at the same path.
func socketDir(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "sock")
}

// socketFile is the path the daemon listens on, inside socketDir. The public
// SocketPath is a symlink to it, so clients outside the sandbox keep the old
// path.
func socketFile(socketPath string) string {
	return filepath.Join(socketDir(socketPath), filepath.Base(socketPath))
}

// ClaudeSessionsDir is where Claude Code writes one file per live session.
func ClaudeSessionsDir() string {
	if d := os.Getenv("AGENT_NET_CLAUDE_SESSIONS_DIR"); d != "" {
		return d
	}
	return filepath.Join(home(), ".claude", "sessions")
}

// PiSessionsDir is where Pi writes its session files. Pi's own environment
// variable is used when it is set, so the daemon reads the same folder as the
// Pi processes it starts.
func PiSessionsDir() string {
	if d := os.Getenv("PI_CODING_AGENT_SESSION_DIR"); d != "" {
		return d
	}
	return filepath.Join(home(), ".pi", "agent", "sessions")
}
