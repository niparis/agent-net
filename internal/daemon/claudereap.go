package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Claude Code keeps a pool of spare background processes. A `claude --bg` job
// claims one, and the claimed process keeps the command line "bg-spare" for the
// rest of its life. When agent-net stops such a job it runs `claude stop <id>`,
// which asks Claude's own supervisor to end it. That is a polite request: the
// process tree can still be there afterwards.
//
// The supervisor's own truth is ~/.claude/daemon/roster.json, written whenever
// its workers change and fresh within seconds. Every pid in the roster is a
// live session or worker, including idle interactive sessions whose
// ~/.claude/jobs/<id>/state.json says "done". So the roster is the first and
// strongest check: a tree may be ended only when its pty host has left a fresh
// roster. A command name or an age is never evidence by itself.
const (
	// claudeStopCheckDelay is how long after `claude stop` the tree is checked.
	claudeStopCheckDelay = 10 * time.Second
	// claudeRosterMaxAge is how old a roster may be and still be trusted.
	claudeRosterMaxAge = 60 * time.Second
	// claudeTreeGrace is how long a tree gets after SIGTERM before SIGKILL.
	claudeTreeGrace = 2 * time.Second
)

// claudeBgProc is what the end-of-job check needs to know about one process.
type claudeBgProc struct {
	PID, PPID int
	UID, EUID int
	Command   string
	Started   time.Time
	Token     string
	RSSKB     int64
}

// claudeBgTree is one bg-pty-host and the bg-spare processes below it.
type claudeBgTree struct {
	Root    int
	Procs   []claudeBgProc
	Started time.Time
	RSSKB   int64
}

// job returns the job the tree serves, or "" when no job claimed it.
func (t claudeBgTree) job(byPID map[int]string) string {
	for _, p := range t.Procs {
		if job := byPID[p.PID]; job != "" {
			return job
		}
	}
	return ""
}

// claudeRoster is the supervisor's list of live workers.
type claudeRoster struct {
	SupervisorPID int                          `json:"supervisorPid"`
	UpdatedAt     int64                        `json:"updatedAt"`
	Workers       map[string]claudeRosterEntry `json:"workers"`
}

type claudeRosterEntry struct {
	PID           int    `json:"pid"`
	ProcStart     string `json:"procStart"`
	ReplPID       int    `json:"replPid"`
	ReplProcStart string `json:"replProcStart"`
}

// claudeRoster reads the supervisor's roster when it is fresh. A missing,
// unreadable or stale roster is never used to decide that something is dead.
func (d *Daemon) claudeRoster() (claudeRoster, bool) {
	if d.cfg.ClaudeSessionsDir == "" {
		return claudeRoster{}, false
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "daemon", "roster.json"))
	if err != nil {
		return claudeRoster{}, false
	}
	var r claudeRoster
	if json.Unmarshal(raw, &r) != nil || r.UpdatedAt <= 0 {
		return claudeRoster{}, false
	}
	age := d.claudeBgNow().Sub(time.UnixMilli(r.UpdatedAt))
	if age < -claudeRosterMaxAge || age > claudeRosterMaxAge {
		return claudeRoster{}, false
	}
	return r, true
}

// readClaudeBgProcs lists the Claude background processes of this machine. It
// reads the argument vector first, so the full process view is read only for
// the few processes that can be a bg-pty-host or a bg-spare.
func readClaudeBgProcs() []claudeBgProc {
	var out []claudeBgProc
	for _, pid := range processList() {
		args := processArgs(pid)
		if len(args) == 0 || !claudeBgCommand(strings.Join(args, " ")) {
			continue
		}
		v, err := readProcView(pid)
		if err != nil {
			continue
		}
		out = append(out, claudeBgProc{PID: v.PID, PPID: v.PPID, UID: v.UID, EUID: v.EUID,
			Command: v.Command, Started: v.Started, Token: v.Token, RSSKB: v.RSSKB})
	}
	return out
}

func (d *Daemon) claudeBgProcs() []claudeBgProc {
	if d.cfg.testClaudeProcs != nil {
		return d.cfg.testClaudeProcs()
	}
	return readClaudeBgProcs()
}

func (d *Daemon) claudeBgNow() time.Time {
	if d.cfg.testClaudeNow != nil {
		return d.cfg.testClaudeNow()
	}
	return time.Now()
}

// claudeBgCommand tells if a command line is a Claude background process.
func claudeBgCommand(cmd string) bool {
	return claudeBgPtyHost(cmd) || claudeBgSpare(cmd)
}

func claudeBgPtyHost(cmd string) bool { return claudeBgFlag(cmd, "--bg-pty-host") }

func claudeBgSpare(cmd string) bool { return claudeBgFlag(cmd, "--bg-spare") }

func claudeBgFlag(cmd, flag string) bool {
	f := strings.Fields(cmd)
	if len(f) == 0 || !claudeProgram(f[0]) {
		return false
	}
	for _, a := range f[1:] {
		if a == flag {
			return true
		}
	}
	return false
}

func claudeProgram(path string) bool {
	return strings.EqualFold(filepath.Base(path), "claude")
}

// claudeBgTrees groups the background processes by their pty host. A tree holds
// the host and the bg-spare descendants below it; other children are not
// touched.
func claudeBgTrees(procs []claudeBgProc) []claudeBgTree {
	byPID := make(map[int]claudeBgProc, len(procs))
	children := make(map[int][]int, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p.PID)
	}
	var out []claudeBgTree
	seen := map[int]bool{}
	for _, p := range procs {
		if !claudeBgPtyHost(p.Command) {
			continue
		}
		t := claudeBgTree{Root: p.PID, Started: p.Started, RSSKB: p.RSSKB, Procs: []claudeBgProc{p}}
		seen[p.PID] = true
		var walk func(pid int)
		walk = func(pid int) {
			for _, id := range children[pid] {
				cp := byPID[id]
				if !claudeBgSpare(cp.Command) {
					continue
				}
				t.Procs = append(t.Procs, cp)
				seen[cp.PID] = true
				t.RSSKB += cp.RSSKB
				if !cp.Started.IsZero() && (t.Started.IsZero() || cp.Started.Before(t.Started)) {
					t.Started = cp.Started
				}
				walk(id)
			}
		}
		walk(p.PID)
		out = append(out, t)
	}
	// A spare whose host died is still a Claude background process, and its
	// job and state files are read the same way.
	for _, p := range procs {
		if seen[p.PID] || claudeBgPtyHost(p.Command) || !claudeBgSpare(p.Command) {
			continue
		}
		out = append(out, claudeBgTree{Root: p.PID, Started: p.Started, RSSKB: p.RSSKB, Procs: []claudeBgProc{p}})
	}
	return out
}

// claudeSessionJobs maps a background process ID to the job that claimed it.
func claudeSessionJobs(sessionsDir string) map[int]string {
	out := map[int]string{}
	if sessionsDir == "" {
		return out
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
		if err != nil {
			continue
		}
		var sf struct {
			PID   int    `json:"pid"`
			JobID string `json:"jobId"`
		}
		if json.Unmarshal(raw, &sf) != nil || sf.PID <= 1 || sf.JobID == "" {
			continue
		}
		out[sf.PID] = sf.JobID
	}
	return out
}

// claudeJobState reads ~/.claude/jobs/<job>/state.json.
func claudeJobState(sessionsDir, job string) (string, bool) {
	if sessionsDir == "" || job == "" || filepath.Base(job) != job {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(sessionsDir), "jobs", job, "state.json"))
	if err != nil {
		return "", false
	}
	var st struct {
		State string `json:"state"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return "", false
	}
	return st.State, true
}

func claudeJobSettled(state string) bool {
	return state == "stopped" || state == "done" || state == "failed"
}

// afterClaudeStop records the tree of a job that agent-net is about to stop,
// then checks within about ten seconds that it is gone and ends what is left.
func (d *Daemon) afterClaudeStop(job string) {
	if job == "" || filepath.Base(job) != job {
		return
	}
	before := d.claudeBgPIDsForJob(job)
	d.goBackground(func() {
		delay := claudeStopCheckDelay
		if d.cfg.testClaudeStopCheckAt > 0 {
			delay = d.cfg.testClaudeStopCheckAt
		}
		var done <-chan struct{}
		if d.ctoCtx != nil {
			done = d.ctoCtx.Done()
		}
		select {
		case <-done:
			return
		case <-time.After(delay):
		}
		d.finishClaudeStop(job, before)
	})
}

// claudeBgPIDsForJob reads the tree of one job, before the stop removes its
// session file. The start token of each process is kept, so a reused process
// number is not touched.
func (d *Daemon) claudeBgPIDsForJob(job string) map[int]string {
	out := map[int]string{}
	procs := d.claudeBgProcs()
	tokens := make(map[int]string, len(procs))
	for _, p := range procs {
		tokens[p.PID] = p.Token
	}
	if roster, ok := d.claudeRoster(); ok {
		if e, ok := roster.Workers[job]; ok {
			if tok := tokens[e.PID]; tok != "" {
				out[e.PID] = tok
			}
			if tok := tokens[e.ReplPID]; tok != "" {
				out[e.ReplPID] = tok
			}
		}
	}
	jobs := claudeSessionJobs(d.cfg.ClaudeSessionsDir)
	for _, tree := range claudeBgTrees(procs) {
		if tree.job(jobs) != job {
			continue
		}
		for _, p := range tree.Procs {
			out[p.PID] = p.Token
		}
	}
	return out
}

// finishClaudeStop ends the tree of a stopped job if the supervisor no longer
// lists it and the real process tree is still there.
func (d *Daemon) finishClaudeStop(job string, before map[int]string) {
	roster, fresh := d.claudeRoster()
	if !fresh {
		d.Logf("claude.tree job=%s check=after-stop action=kept reason=roster-stale", job)
		return
	}
	if _, listed := roster.Workers[job]; listed {
		d.Logf("claude.tree job=%s check=after-stop action=kept reason=roster", job)
		return
	}
	for _, e := range roster.Workers {
		if _, ok := before[e.PID]; ok && e.PID > 0 {
			d.Logf("claude.tree job=%s check=after-stop action=kept reason=roster-pid pid=%d", job, e.PID)
			return
		}
		if _, ok := before[e.ReplPID]; ok && e.ReplPID > 0 {
			d.Logf("claude.tree job=%s check=after-stop action=kept reason=roster-pid pid=%d", job, e.ReplPID)
			return
		}
	}
	state, ok := claudeJobState(d.cfg.ClaudeSessionsDir, job)
	if !ok || !claudeJobSettled(state) {
		d.Logf("claude.tree job=%s check=after-stop action=kept state=%s", job, state)
		return
	}
	jobs := claudeSessionJobs(d.cfg.ClaudeSessionsDir)
	for _, tree := range claudeBgTrees(d.claudeBgProcs()) {
		match := tree.job(jobs) == job
		if !match {
			for _, p := range tree.Procs {
				if token, ok := before[p.PID]; ok && token != "" && token == p.Token {
					match = true
					break
				}
			}
		}
		if !match {
			continue
		}
		if d.claudeTreeRefusal(tree) != "" {
			continue
		}
		d.endClaudeBgTree(tree, job)
	}
}

// claudeTreeRefusal says why a tree must not be ended, or returns "".
func (d *Daemon) claudeTreeRefusal(tree claudeBgTree) string {
	uid := os.Getuid()
	for _, p := range tree.Procs {
		if p.PID <= 1 || p.PID == os.Getpid() {
			return "it is the init process or this daemon"
		}
		if p.UID != uid || p.EUID != uid {
			return fmt.Sprintf("it belongs to another user (uid %d)", p.UID)
		}
		if isAgentNetService(p.Command) {
			return "it is the agent-net daemon or gateway"
		}
		if reason := d.knownAgentNetProcess(p.PID); reason != "" {
			return reason
		}
	}
	return ""
}

// endClaudeBgTree sends TERM to the tree, waits, then sends KILL to what is
// still the same run. It writes one log line for the ended tree.
func (d *Daemon) endClaudeBgTree(tree claudeBgTree, job string) {
	tokens := make(map[int]string, len(tree.Procs))
	var pids []int
	for _, p := range tree.Procs {
		if p.Token == "" {
			continue
		}
		tokens[p.PID] = p.Token
		pids = append(pids, p.PID)
	}
	if len(pids) == 0 {
		return
	}
	grace := claudeTreeGrace
	if d.cfg.testProcessStopGrace > 0 {
		grace = d.cfg.testProcessStopGrace
	}
	var termed []int
	for _, pid := range pids {
		if !d.claudeBgSameRun(pid, tokens[pid]) {
			continue // the process number now belongs to another program
		}
		if d.sendProcessSignal(pid, syscall.SIGTERM) == nil {
			termed = append(termed, pid)
		}
	}
	d.waitClaudeBgGone(termed, tokens, grace)
	var killed []int
	for _, pid := range termed {
		if !d.claudeBgSameRun(pid, tokens[pid]) {
			continue
		}
		if d.sendProcessSignal(pid, syscall.SIGKILL) == nil {
			killed = append(killed, pid)
		}
	}
	d.waitClaudeBgGone(killed, tokens, processKillWait)

	outcome := "term"
	for _, pid := range killed {
		if d.claudeBgSameRun(pid, tokens[pid]) {
			outcome = "failed"
		} else {
			outcome = "kill"
		}
	}
	age := time.Duration(0)
	if !tree.Started.IsZero() {
		age = d.claudeBgNow().Sub(tree.Started)
	}
	if age < 0 {
		age = 0
	}
	d.Logf("claude.tree job=%s pid=%d age=%s rss=%s ended=%s", job, tree.Root, age.Round(time.Minute), claudeRSSText(tree.RSSKB), outcome)
}

// claudeBgSameRun tells if pid is alive and is the run that was read.
func (d *Daemon) claudeBgSameRun(pid int, token string) bool {
	if token == "" {
		return false
	}
	for _, p := range d.claudeBgProcs() {
		if p.PID == pid {
			return p.Token == token
		}
	}
	return false
}

func (d *Daemon) waitClaudeBgGone(pids []int, tokens map[int]string, max time.Duration) {
	if len(pids) == 0 {
		return
	}
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		alive := false
		for _, pid := range pids {
			if d.claudeBgSameRun(pid, tokens[pid]) {
				alive = true
				break
			}
		}
		if !alive {
			return
		}
		time.Sleep(processPoll)
	}
}

func claudeRSSText(kb int64) string {
	switch {
	case kb >= 1<<20:
		return fmt.Sprintf("%.1f GB", float64(kb)/(1<<20))
	case kb >= 1024:
		return fmt.Sprintf("%.0f MB", float64(kb)/1024)
	default:
		return fmt.Sprintf("%d KB", kb)
	}
}
