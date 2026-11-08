package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// A session's registered process can be missing or stale: a Codex thread runs on
// the shared app server and has no process of its own, and a token can be
// empty when the process could not be read at discovery. Before a stop gives
// up, findSessionProcess looks for the harness process again by a safe
// identity. It never takes a process that only has the right ID.

// codexOtherCommands are the Codex commands that are not an interactive window.
var codexOtherCommands = map[string]bool{
	"app-server": true, "proxy": true, "exec": true, "e": true, "mcp": true, "mcp-server": true,
	"login": true, "logout": true, "completion": true, "apply": true, "a": true, "review": true,
	"cloud": true, "sandbox": true, "features": true, "remote-control": true, "help": true,
	"debug": true, "plugin": true,
}

// harnessWord says whether args start a harness program: its binary, or an
// interpreter that runs its script ("node .../bin/pi", "sh .../codex").
func harnessWord(harness string, args []string) bool {
	for i := 0; i < len(args) && i < 2; i++ {
		base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(args[i]), ".js"), ".mjs")
		if base == harness {
			return true
		}
	}
	return false
}

// sessionProcessMatch says how well pid fits the session: 2 when its arguments
// carry the native session ID, 1 when only the harness and the folder fit, 0
// when it is not this session's process.
func sessionProcessMatch(info protocol.SessionInfo, pid int) int {
	args := processArgs(pid)
	if len(args) == 0 || !harnessWord(info.Harness, args) {
		return 0
	}
	if info.Harness == "codex" {
		for _, a := range args[1:] {
			if codexOtherCommands[a] {
				return 0
			}
		}
	}
	if info.NativeID != "" {
		for _, a := range args {
			if a == info.NativeID || strings.HasSuffix(a, info.NativeID) && strings.HasPrefix(a, "--") && strings.Contains(a, "=") {
				return 2
			}
		}
	}
	if info.Cwd != "" && filepath.Clean(agyProcessCwd(pid)) == filepath.Clean(info.Cwd) {
		return 1
	}
	return 0
}

// findSessionProcess returns the one live process of this session and its
// token. peers is how many local sessions of the same harness share the folder:
// a match by folder alone is only safe when the session is alone there.
func (d *Daemon) findSessionProcess(info protocol.SessionInfo, peers int) (pid int, token string, ok bool) {
	self := os.Getpid()
	var best, found []int
	level := 0
	candidates := processList()
	if info.PID > 1 {
		candidates = append(candidates, info.PID)
	}
	seen := map[int]bool{}
	for _, p := range candidates {
		if p == self || seen[p] {
			continue
		}
		seen[p] = true
		m := sessionProcessMatch(info, p)
		if m == 0 || (m == 1 && peers > 1) {
			continue
		}
		if m > level {
			level, best = m, nil
		}
		if m == level {
			best = append(best, p)
		}
	}
	// A launcher and the program it runs both match: keep the innermost.
	for _, p := range best {
		inner := false
		for _, q := range best {
			for c, depth := q, 0; c > 1 && depth < 64 && !inner; depth++ {
				parent, err := processParentPID(c)
				if err != nil {
					break
				}
				if parent == p && q != p {
					inner = true
				}
				c = parent
			}
		}
		if !inner {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		return 0, "", false
	}
	tok, live := procToken(found[0])
	if !live || tok == "" {
		return 0, "", false
	}
	return found[0], tok, true
}

// stopTarget gives the process to stop and the token that was current for it.
// The registered process is used when it still is the same run. Otherwise the
// session's process is looked up again.
func (d *Daemon) stopTarget(ls *localSession, info protocol.SessionInfo) (int, string, error) {
	d.mu.Lock()
	token := ls.stopToken
	peers := 0
	for _, o := range d.sessions {
		if o.info.Harness == info.Harness && o.info.Worker == nil && filepath.Clean(o.info.Cwd) == filepath.Clean(info.Cwd) {
			peers++
		}
	}
	d.mu.Unlock()
	if current, live := procToken(info.PID); info.PID > 1 && token != "" && live && current == token {
		return info.PID, token, nil
	}
	if pid, tok, ok := d.findSessionProcess(info, peers); ok {
		return pid, tok, nil
	}
	return 0, "", fmt.Errorf("agent-net cannot tell which program runs this %s session. Close its terminal window or herdr pane yourself, then press v to see ended sessions", info.Harness)
}
