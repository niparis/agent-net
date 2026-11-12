package daemon

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/usage"
)

// Why a process belongs to an owner (MachineProcRow.OwnerRule). Each rule is a
// fact the daemon or the operating system holds. A command name alone is
// never enough, except for the gateway, whose own command line is
// "agent-net gateway".
const (
	ruleDaemonPID      = "daemon pid"
	ruleSessionPID     = "registered session pid"
	ruleWorkerPID      = "registered worker pid"
	ruleMCPGroup       = "mcp server process group"
	ruleRosterPID      = "Claude roster pid"
	ruleWorktreeCWD    = "cwd in worker worktree"
	ruleGatewayCommand = "command line agent-net gateway"
)

// attributeProcs names the owner of each process in procs and says which rule
// did it, in the same order. The rules, first match wins:
//
//  1. the pid is one the daemon registered (itself, a session, a worker, a
//     hosted MCP server) or one in the Claude supervisor's roster;
//  2. an ancestor of the process has such a pid;
//  3. the working directory of the process is inside the folder of a worker's
//     git worktree, which is how a "claude bg-spare" process shows its worker.
//
// roster is nil when the Claude roster is not fresh. probe, when set, limits
// which processes have their working directory read.
func (d *Daemon) attributeProcs(procs []usage.MachineProc, roster *claudeRoster, probe func(pid int) bool) []procOwner {
	owners := d.procOwners()
	d.addRosterOwners(owners, roster)
	out := make([]procOwner, len(procs))
	var unknown []int
	for i, p := range procs {
		out[i] = classifyProc(p, owners)
		if out[i].kind == protocol.ProcOwnerOther && (probe == nil || probe(p.PID)) {
			unknown = append(unknown, p.PID)
		}
	}
	if len(unknown) == 0 || d.worktreesDir() == "" {
		return out
	}
	cwds := processCWDs(unknown)
	for i, p := range procs {
		if out[i].kind != protocol.ProcOwnerOther {
			continue
		}
		if id := d.workerIDFromCWD(cwds[p.PID]); id != "" {
			out[i] = d.workerOwner(id, ruleWorktreeCWD)
		}
	}
	return out
}

// addRosterOwners adds the processes of Claude's live background sessions that
// no earlier rule named. A session in a worker's worktree is that worker's, a
// session agent-net registered keeps its id, and any other is a Claude
// background session known by its short id.
func (d *Daemon) addRosterOwners(owners map[int]procOwner, roster *claudeRoster) {
	if roster == nil || len(roster.Workers) == 0 {
		return
	}
	byNative := map[string]procOwner{}
	d.mu.Lock()
	for _, ls := range d.sessions {
		if ls.info.Harness != "claude" || ls.info.NativeID == "" {
			continue
		}
		name := ls.info.Name
		if name == "" {
			name = "Claude session"
		}
		if w := ls.info.Worker; w != nil && w.ID != "" {
			byNative[ls.info.NativeID] = procOwner{protocol.ProcOwnerWorker, name, w.ID, ruleRosterPID}
			continue
		}
		byNative[ls.info.NativeID] = procOwner{protocol.ProcOwnerSession, name, ls.sessionID, ruleRosterPID}
	}
	d.mu.Unlock()
	shorts := make([]string, 0, len(roster.Workers))
	for short := range roster.Workers {
		shorts = append(shorts, short)
	}
	sort.Strings(shorts)
	for _, short := range shorts {
		w := roster.Workers[short]
		var o procOwner
		if id := d.workerIDFromCWD(w.CWD); id != "" {
			o = d.workerOwner(id, ruleRosterPID)
		} else if known, ok := byNative[w.SessionID]; ok && w.SessionID != "" {
			o = known
		} else {
			o = procOwner{protocol.ProcOwnerSession, "Claude background session", short, ruleRosterPID}
		}
		for _, pid := range []int{w.PID, w.ReplPID} {
			if _, taken := owners[pid]; pid > 1 && !taken {
				owners[pid] = o
			}
		}
	}
}

// workerOwner is the owner record of worker id: its title when this daemon
// still runs it, else just its id.
func (d *Daemon) workerOwner(id, rule string) procOwner {
	name := "worker " + id
	d.wmu.Lock()
	if w := d.workers[id]; w != nil {
		if w.title != "" {
			name = w.title
		} else if w.project != "" {
			name = w.project
		}
	}
	d.wmu.Unlock()
	return procOwner{protocol.ProcOwnerWorker, name, id, rule}
}

// workerIDFromCWD returns the worker whose worktree holds cwd, or "". A
// worktree is <state>/worktrees/<worker id>; a folder that only shares the
// start of that path does not count. The operating system may report the real
// path of a link, so the real path of the worktrees folder is tried too.
func (d *Daemon) workerIDFromCWD(cwd string) string {
	root := d.worktreesDir()
	if cwd == "" || root == "" {
		return ""
	}
	cwd = filepath.Clean(cwd)
	roots := []string{root}
	if real, err := filepath.EvalSymlinks(root); err == nil && real != root {
		roots = append(roots, real)
	}
	for _, r := range roots {
		rel, err := filepath.Rel(r, cwd)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		first, _, _ := strings.Cut(rel, string(filepath.Separator))
		if strings.HasPrefix(first, "w_") && len(first) > 2 {
			return first
		}
	}
	return ""
}
