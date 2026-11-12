package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Entering a session that runs in herdr (decision 0002, W16; herdr is a
// terminal multiplexer for coding agents).
//
// herdr is client/server. The panes live in the server, and the owner's
// terminal is a client. Every process in a pane has HERDR_ENV=1, HERDR_PANE_ID
// and HERDR_SOCKET_PATH (the server) in its environment. When macOS does not
// expose those variables, the daemon can identify a herdr server in the
// process ancestry and probe its socket. The normal path focuses that pane,
// although an already attached client may stay on its previous tab. The
// direct attach command remains the fallback for clients without Herdr.
//
// Two direct attaches cannot share a terminal. Without --takeover the second
// one fails with a clear error and the first stays. With --takeover the first
// one is thrown out. The owner's normal herdr client is not a direct attach,
// so it is not touched either way. The plan never takes over: it must not
// drop an attach the owner opened on purpose.

// herdrPane is where a session's process runs in herdr.
type herdrPane struct {
	id, socket, bin, session string
}

var herdrIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]{0,63}$`)
var herdrSessionRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// findHerdrPane reads the herdr variables of pid, or of the closest parent that
// has them (a launcher may have cleaned the environment of the child).
func findHerdrPane(pid int) (herdrPane, bool) {
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		var h herdrPane
		isHerdr := false
		for _, kv := range processEnviron(p) {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "HERDR_ENV":
				isHerdr = v == "1"
			case "HERDR_PANE_ID":
				h.id = v
			case "HERDR_SOCKET_PATH":
				h.socket = v
			case "HERDR_BIN_PATH":
				h.bin = v
			}
		}
		if isHerdr && herdrIDRe.MatchString(h.id) {
			if h.socket != "" && !filepath.IsAbs(h.socket) {
				h.socket = ""
			}
			return h, true
		}
		parent, err := processParentPID(p)
		if err != nil {
			break
		}
		p = parent
	}
	return herdrPane{}, false
}

// findHerdrServer finds a Herdr server in pid's parent chain when the process
// environments do not expose Herdr's pane variables.
func findHerdrServer(pid int) (herdrPane, bool) {
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		if h, ok := herdrServerArgs(processArgs(p)); ok {
			return h, true
		}
		parent, err := processParentPID(p)
		if err != nil {
			break
		}
		p = parent
	}
	return herdrPane{}, false
}

func herdrServerArgs(args []string) (herdrPane, bool) {
	if len(args) < 2 || args[1] != "server" || filepath.Base(args[0]) != "herdr" {
		return herdrPane{}, false
	}
	h := herdrPane{bin: args[0]}
	for i := 2; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--socket=") {
			h.socket = strings.TrimPrefix(a, "--socket=")
		} else if strings.HasPrefix(a, "--socket-path=") {
			h.socket = strings.TrimPrefix(a, "--socket-path=")
		} else if (a == "--socket" || a == "--socket-path" || a == "-S") && i+1 < len(args) {
			i++
			h.socket = args[i]
		} else if a == "--session" && i+1 < len(args) {
			i++
			h.session = args[i]
		} else if strings.HasPrefix(a, "--session=") {
			h.session = strings.TrimPrefix(a, "--session=")
		}
	}
	if h.socket != "" && !filepath.IsAbs(h.socket) {
		h.socket = ""
	}
	if !herdrSessionRe.MatchString(h.session) {
		h.session = ""
	}
	return h, true
}

// herdrSocketCandidates returns explicit server sockets first, then Herdr's
// documented config socket and common per-user config locations. A candidate
// is accepted only after the server answers the pane-list API.
func herdrSocketCandidates(h herdrPane) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if filepath.IsAbs(p) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(h.socket)

	var configDirs []string
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		configDirs = append(configDirs, xdg)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		configDirs = append(configDirs,
			filepath.Join(home, ".config"),
			filepath.Join(home, "Library", "Application Support"),
		)
	}
	for _, dir := range configDirs {
		if h.session != "" {
			add(filepath.Join(dir, "herdr", "sessions", h.session, "herdr.sock"))
		}
		add(filepath.Join(dir, "herdr", "herdr.sock"))
	}
	return out
}

func (d *Daemon) herdrPanesFromServer(bin string, h herdrPane) ([]herdrPaneInfo, string, bool) {
	for _, socket := range herdrSocketCandidates(h) {
		panes, err := d.herdrPanes(bin, socket)
		if err == nil {
			return panes, socket, true
		}
	}
	return nil, "", false
}

// herdrBinary honors an explicit pane hint, then uses the harness search.
func (d *Daemon) herdrBinary(hint string) string {
	if filepath.IsAbs(hint) && isExecutable(hint) {
		return hint
	}
	bin, _ := d.lookupHerdrBinary()
	return bin
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}

// herdrRun runs a herdr command against one server and returns its output.
func (d *Daemon) herdrRun(bin, socket string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "HERDR_SESSION=") && !strings.HasPrefix(kv, "HERDR_SOCKET_PATH=") {
			env = append(env, kv)
		}
	}
	if socket != "" {
		env = append(env, "HERDR_SOCKET_PATH="+socket)
	}
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		d.invalidateBinary("herdr", bin)
	}
	return out, err
}

type herdrPaneInfo struct {
	PaneID     string `json:"pane_id"`
	TabID      string `json:"tab_id"`
	TerminalID string `json:"terminal_id"`
	Agent      string `json:"agent"`
	Focused    bool   `json:"focused"`
	Session    *struct {
		Agent string `json:"agent"`
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"agent_session"`
}

// herdrPanes lists the panes of a server.
func (d *Daemon) herdrPanes(bin, socket string) ([]herdrPaneInfo, error) {
	out, err := d.herdrRun(bin, socket, "pane", "list")
	if err != nil {
		return nil, err
	}
	var r struct {
		Result struct {
			Panes *[]herdrPaneInfo `json:"panes"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, err
	}
	if r.Result.Panes == nil {
		return nil, fmt.Errorf("herdr pane list response has no panes")
	}
	return *r.Result.Panes, nil
}

// herdrHolds says whether the pane runs one of the pids (its shell, or a
// process of its foreground group).
func (d *Daemon) herdrHolds(bin, socket, pane string, pids map[int]bool) bool {
	out, err := d.herdrRun(bin, socket, "pane", "process-info", "--pane", pane)
	if err != nil {
		return false
	}
	var r struct {
		Result struct {
			Info struct {
				ShellPID int `json:"shell_pid"`
				Procs    []struct {
					PID int `json:"pid"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &r) != nil {
		return false
	}
	if r.Result.Info.ShellPID > 1 && pids[r.Result.Info.ShellPID] {
		return true
	}
	for _, p := range r.Result.Info.Procs {
		if p.PID > 1 && pids[p.PID] {
			return true
		}
	}
	return false
}

// herdrPaneProcesses lists the pids of a pane: its shell and the processes of
// its foreground group.
func (d *Daemon) herdrPaneProcesses(bin, socket, pane string) []int {
	out, err := d.herdrRun(bin, socket, "pane", "process-info", "--pane", pane)
	if err != nil {
		return nil
	}
	var r struct {
		Result struct {
			Info struct {
				ShellPID int `json:"shell_pid"`
				Procs    []struct {
					PID int `json:"pid"`
				} `json:"foreground_processes"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &r) != nil {
		return nil
	}
	var pids []int
	if r.Result.Info.ShellPID > 1 {
		pids = append(pids, r.Result.Info.ShellPID)
	}
	for _, p := range r.Result.Info.Procs {
		if p.PID > 1 {
			pids = append(pids, p.PID)
		}
	}
	return pids
}

// pidChain is pid and its parents.
func pidChain(pid int) map[int]bool {
	chain := map[int]bool{}
	for p, depth := pid, 0; p > 1 && depth < 64; depth++ {
		chain[p] = true
		parent, err := processParentPID(p)
		if err != nil {
			break
		}
		p = parent
	}
	return chain
}

// herdrPlan finds the herdr pane of a session. ok is false when
// the session does not run in herdr. When ok is
// true the plan is final, with a command or with a note that says why not.
// Pi uses it because it has no attach client: its view is the pane it runs in.
func (d *Daemon) herdrPlan(info protocol.SessionInfo, q protocol.EnterRequest, plan protocol.EnterPlan, name string) (protocol.EnterPlan, bool) {
	h, found := findHerdrPane(info.PID)
	fromServer := false
	if !found {
		h, found = findHerdrServer(info.PID)
		fromServer = found
	}
	if !found {
		return protocol.EnterPlan{}, false
	}
	where := firstNonEmpty(plan.Host, "its machine")
	note := func(format string, a ...any) (protocol.EnterPlan, bool) {
		return protocol.EnterPlan{Host: plan.Host, Note: fmt.Sprintf(format, a...)}, true
	}
	bin := d.herdrBinary(h.bin)
	if bin == "" {
		return note("%s runs in herdr, but herdr is not installed where agent-net can find it: open its window on %s", name, where)
	}
	var panes []herdrPaneInfo
	if fromServer {
		var ok bool
		panes, h.socket, ok = d.herdrPanesFromServer(bin, h)
		if !ok {
			return note("%s has a herdr server above its process, but no candidate herdr socket answers: open its window on %s", name, where)
		}
	} else {
		var err error
		panes, err = d.herdrPanes(bin, h.socket)
		if err != nil {
			return note("%s runs in herdr, but the herdr server does not answer: open its window on %s", name, where)
		}
	}
	// The pane in the environment is the one the process started in. A moved
	// pane gets a new ID, so check that the pane still holds the process, and
	// look at the other panes if it does not.
	pids := pidChain(info.PID)
	var pane *herdrPaneInfo
	for i := range panes {
		if panes[i].PaneID == h.id && d.herdrHolds(bin, h.socket, h.id, pids) {
			pane = &panes[i]
			break
		}
	}
	for i := 0; pane == nil && i < len(panes); i++ {
		if panes[i].PaneID != h.id && d.herdrHolds(bin, h.socket, panes[i].PaneID, pids) {
			pane = &panes[i]
		}
	}
	if pane == nil {
		return note("%s was in herdr pane %s, which has closed, and no other pane of that herdr server holds it: open its window on %s", name, h.id, where)
	}
	argv := []string{"env", "-u", "HERDR_SESSION"}
	if h.socket != "" {
		argv = append(argv, "HERDR_SOCKET_PATH="+h.socket)
	}
	// The asking machine has a herdr client: focus the pane there, so the
	// client opens on it. This is best effort; the note names the pane in case
	// the client opens on another tab.
	if q.HerdrClient {
		var err error
		if pane.Agent != "" {
			_, err = d.herdrRun(bin, h.socket, "agent", "focus", pane.PaneID)
		} else if herdrIDRe.MatchString(pane.TabID) {
			_, err = d.herdrRun(bin, h.socket, "tab", "focus", pane.TabID)
		} else {
			err = fmt.Errorf("pane has no tab ID")
		}
		if err != nil {
			return note("cannot focus %s in herdr; open pane %s on %s", name, pane.PaneID, where)
		}
	}
	switch {
	case pane.Agent != "" && herdrIDRe.MatchString(pane.PaneID):
		argv = append(argv, bin, "agent", "attach", pane.PaneID)
	case herdrIDRe.MatchString(pane.TerminalID): // herdr does not know an agent there: attach the terminal
		argv = append(argv, bin, "terminal", "attach", pane.TerminalID)
	default:
		return note("%s runs in herdr pane %s, which cannot be attached: open its window on %s", name, pane.PaneID, where)
	}
	out := protocol.EnterPlan{
		Argv: argv, Host: plan.Host,
		Note: fmt.Sprintf("Press ctrl+] to come back here; %s keeps running. If herdr says the terminal already has an attached client, close that other attach first.", name),
	}
	if q.HerdrClient && herdrIDRe.MatchString(pane.PaneID) {
		out.HerdrPane = pane.PaneID
	}
	return out, true
}

// AttachClientsViewing says, for each `claude attach` client pid, whether the
// owner is looking at it (idle compaction, decision 0078). A client is viewed
// when it runs outside herdr, or in a herdr pane that herdr reports as focused.
// A client in an unfocused pane is not. When herdr cannot say where a client
// is, the owner may be looking at it, so it counts as viewed.
//
// One scan costs one `pane list` per herdr server and at most one
// `pane process-info` per pane, however many clients there are.
func (d *Daemon) AttachClientsViewing(pids []int) map[int]bool {
	type server struct {
		bin, socket string
		panes       []herdrPaneInfo
		ok          bool
		procs       map[string]map[int]bool // pane -> its pids, read once
	}
	servers := map[string]*server{}
	holds := func(sv *server, pane string, chain map[int]bool) bool {
		set, seen := sv.procs[pane]
		if !seen {
			set = map[int]bool{}
			for _, p := range d.herdrPaneProcesses(sv.bin, sv.socket, pane) {
				set[p] = true
			}
			sv.procs[pane] = set
		}
		for p := range set {
			if chain[p] {
				return true
			}
		}
		return false
	}
	out := make(map[int]bool, len(pids))
	for _, pid := range pids {
		out[pid] = true // unless a focused:false pane is found below
		h, found := findHerdrPane(pid)
		fromServer := false
		if !found {
			h, found = findHerdrServer(pid)
			fromServer = found
		}
		if !found {
			continue // outside herdr
		}
		key := h.bin + "|" + h.socket + "|" + h.session
		sv := servers[key]
		if sv == nil {
			sv = &server{procs: map[string]map[int]bool{}}
			servers[key] = sv
			if bin := d.herdrBinary(h.bin); bin != "" {
				sv.bin, sv.socket = bin, h.socket
				if fromServer {
					sv.panes, sv.socket, sv.ok = d.herdrPanesFromServer(bin, h)
				} else if panes, err := d.herdrPanes(bin, h.socket); err == nil {
					sv.panes, sv.ok = panes, true
				}
			}
		}
		if !sv.ok {
			continue
		}
		// The pane in the environment is where the client started; a moved pane
		// has a new ID, so look at the other panes only if it no longer holds it.
		chain := pidChain(pid)
		for _, own := range []bool{true, false} {
			done := false
			for _, p := range sv.panes {
				if (p.PaneID == h.id) == own && holds(sv, p.PaneID, chain) {
					out[pid], done = p.Focused, true
					break
				}
			}
			if done {
				break
			}
		}
	}
	return out
}
