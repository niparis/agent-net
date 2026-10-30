package daemon

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Hostname is the name that shows for this machine in `agent-net nodes` and
// `list`. A name set by hand in node.json ("hostname") wins. On macOS the
// result of os.Hostname() changes with the network (DHCP and mDNS give names
// like "Laptop-1.local"), so the user-set LocalHostName is used instead. On
// Linux os.Hostname() is used as it is.
func Hostname(pinned string) string {
	return displayHostname(pinned, runtime.GOOS, os.Hostname, scutilGet)
}

func displayHostname(pinned, goos string, osHostname func() (string, error), scutil func(string) (string, error)) string {
	if p := strings.TrimSpace(pinned); p != "" {
		return p
	}
	if goos == "darwin" && scutil != nil {
		if out, err := scutil("LocalHostName"); err == nil {
			if n := strings.TrimSpace(out); n != "" {
				return n
			}
		}
	}
	h, _ := osHostname()
	h = strings.TrimSpace(h)
	if goos == "darwin" {
		// A short name: drop ".local" and any domain.
		if i := strings.IndexByte(h, '.'); i > 0 {
			h = h[:i]
		}
	}
	return h
}

func scutilGet(key string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "scutil", "--get", key).Output()
	return string(out), err
}
