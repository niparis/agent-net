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

func Path() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, shell, "-lc", `printf %s "$PATH"`).Output(); err == nil && len(out) > 0 {
			return string(out)
		}
	}
	home, _ := os.UserHomeDir()
	return strings.Join([]string{os.Getenv("PATH"), "/opt/homebrew/bin", "/usr/local/bin", filepath.Join(home, ".local", "bin")}, string(os.PathListSeparator))
}
