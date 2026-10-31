// Package loginpath finds the user's login-shell executable search path.
package loginpath

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Environment captures the allowlisted settings together from one login shell.
// If it fails, the current process environment is used.
func Environment() []string {
	keys := []string{"PATH", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR", "XDG_STATE_HOME", "OPENCODE_DB", "PI_CODING_AGENT_DIR", "QUOTA_AXI_OPENCODE_GO_PI_AUTH"}
	if shell := os.Getenv("SHELL"); shell != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		script := `printf '%s\0' "$PATH" "$XDG_CACHE_HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_RUNTIME_DIR" "$XDG_STATE_HOME" "$OPENCODE_DB" "$PI_CODING_AGENT_DIR" "$QUOTA_AXI_OPENCODE_GO_PI_AUTH"`
		if out, err := exec.CommandContext(ctx, shell, "-lc", script).Output(); err == nil {
			fields := strings.Split(string(out), "\x00")
			if len(fields) == len(keys)+1 && fields[len(keys)] == "" && fields[0] != "" {
				var env []string
				for i, key := range keys {
					if fields[i] != "" {
						env = append(env, key+"="+fields[i])
					}
				}
				return env
			}
		}
	}
	home, _ := os.UserHomeDir()
	path := strings.Join([]string{os.Getenv("PATH"), "/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin")}, string(os.PathListSeparator))
	env := []string{"PATH=" + path}
	for _, key := range keys[1:] {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func Path() string {
	for _, item := range Environment() {
		if strings.HasPrefix(item, "PATH=") {
			return strings.TrimPrefix(item, "PATH=")
		}
	}
	return ""
}
