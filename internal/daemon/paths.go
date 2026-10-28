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

// ClaudeSessionsDir is where Claude Code writes one file per live session.
func ClaudeSessionsDir() string {
	if d := os.Getenv("AGENT_NET_CLAUDE_SESSIONS_DIR"); d != "" {
		return d
	}
	return filepath.Join(home(), ".claude", "sessions")
}
