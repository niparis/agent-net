package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const restartTabEnv = "AGENT_NET_TUI_RESTART_TAB"
const restartVersionEnv = "AGENT_NET_TUI_RESTART_VERSION"

type restartState struct {
	changed                    func() bool
	lastCheck                  time.Time
	pending, requested, failed bool
	version, target, attempted string
}

func executableChanged(path string) func() bool {
	original, err := os.Stat(path)
	if err != nil {
		return func() bool { return false }
	}
	return func() bool {
		current, err := os.Stat(path)
		return err == nil && (!os.SameFile(original, current) || original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime()))
	}
}

func (m *Model) restartTick() bool {
	r := m.restart
	if r == nil || r.failed {
		return false
	}
	now := m.now()
	if r.lastCheck.IsZero() || now.Sub(r.lastCheck) >= 10*time.Second {
		r.lastCheck = now
		if r.changed != nil && r.changed() {
			r.pending = true
			r.target = m.snap.Version
		}
		if newerVersion(m.snap.Version, r.version) && m.snap.Version != r.attempted {
			r.pending = true
			r.target = m.snap.Version
		}
	}
	if !r.pending || m.mode != modeNormal || m.filtering || m.finder || m.help || m.archive != nil || m.resumeConfirm != nil || m.sessionPending != nil || m.proposalConfirm || m.proposalDetail != nil || m.proposalHistory || m.open != nil || m.inboxDetail || m.inboxEditing || m.delivery.detail || m.mp.finding || m.mp.removal != nil || m.tl.capturing() || m.focus == focusCTO {
		return false
	}
	r.requested = true
	return true
}

func restartExecutable() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return path
}

func restartEnv(tab int, version string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, restartTabEnv+"=") && !strings.HasPrefix(v, restartVersionEnv+"=") {
			env = append(env, v)
		}
	}
	return append(env, restartTabEnv+"="+strconv.Itoa(tab), restartVersionEnv+"="+version)
}

var execRestart = syscall.Exec

func restoreRestart(m *Model, version string) string {
	attempted := os.Getenv(restartVersionEnv)
	if n, err := strconv.Atoi(os.Getenv(restartTabEnv)); err == nil && n >= 0 && n <= deliveryTab {
		m.tab = n
	}
	if v := os.Getenv(restartVersionEnv); v != "" {
		if newerVersion(v, version) {
			m.fail("New version " + v + " installed; press q and start agent-net tui again")
		} else {
			if version != "" && version != "dev" {
				v = version
			}
			m.say(fmt.Sprintf("Updated to %s", v))
		}
	}
	os.Unsetenv(restartTabEnv)
	os.Unsetenv(restartVersionEnv)
	return attempted
}

func newerVersion(a, b string) bool {
	var aa, ab, ac, ba, bb, bc int
	if _, err := fmt.Sscanf(a, "v%d.%d.%d", &aa, &ab, &ac); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(b, "v%d.%d.%d", &ba, &bb, &bc); err != nil {
		return false
	}
	return aa > ba || aa == ba && (ab > bb || ab == bb && ac > bc)
}

func (m Model) execUpdate(path string, args []string, exec func(string, []string, []string) error) error {
	return exec(path, args, restartEnv(m.tab, m.restart.target))
}
