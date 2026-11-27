package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The OS sandbox of a worker (hardening plan W7, docs/design/worker-os-sandbox.md,
// decision 0171 phase 4b, and decision 0197 for what it allows).
//
// A wrapper around the whole harness process of a Pi or OpenCode worker. It is
// on by default (decision of 2026-11-17); node.json on the machine that runs
// the worker can turn it off for a project (workers.os_sandbox_off). It keeps
// one rule: the worker writes only in its worktree, its own temp folder, its
// caches, the repository's git folder and the agent-net socket folder. Reads,
// the network, loopback ports and unix sockets are all open, and nothing is
// hidden. On Linux it is bubblewrap with the host read-only and those folders
// bound writable; on macOS it is a sandbox-exec profile built from the same
// policy (ossandbox_seatbelt.go).
//
// Codex and Antigravity keep their own sandboxes and are not wrapped; they get
// the same writable folders (workerWriteRoots). A Claude worker is `claude
// --bg`, which runs the session in Claude Code's own background service,
// outside any wrapper: with the switch on, it is refused. Its own Bash sandbox
// gets the same folders (claudeWorkerSandbox).
//
// Never a silent start without the sandbox: when the sandbox is on and the
// wrapper cannot be used, the start is refused with the reason and the fix.

// osSandboxMarker is the file in a worker's private folder that records the
// wrapper it was started in, so its follow-ups and a daemon restart keep it.
const osSandboxMarker = "os-sandbox"

// projectList is a list of project names in node.json. "*", as the value or
// as an item, means every project.
type projectList []string

func (p *projectList) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*p = projectList{one}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return errors.New(`a list of project names, or "*" for every project`)
	}
	*p = list
	return nil
}

// osSandboxMode is how node.json treats the OS sandbox for one project.
type osSandboxMode int

const (
	osSandboxDefault  osSandboxMode = iota // on for Pi and OpenCode, as nothing was set
	osSandboxExplicit                      // listed in workers.os_sandbox: on for every harness it can wrap, and the rest refused
	osSandboxOff                           // listed in workers.os_sandbox_off: no wrapper
)

func inProjectList(list []string, project string) bool {
	for _, p := range list {
		if p == "*" || strings.EqualFold(p, project) {
			return true
		}
	}
	return false
}

// osSandboxMode tells how node.json treats the OS sandbox for project. A
// project in both lists is off: turning it off is the owner's last word.
func (s WorkerSettings) osSandboxMode(project string) osSandboxMode {
	switch {
	case inProjectList(s.OSSandboxOff, project):
		return osSandboxOff
	case inProjectList(s.OSSandbox, project):
		return osSandboxExplicit
	}
	return osSandboxDefault
}

// osSandboxDecide is the rule for one start: the wrapper's name, "" when the
// worker runs without it, or the refusal. A worker that was started in the
// wrapper before comes here as osSandboxExplicit; ready checks the tool on
// this machine.
func osSandboxDecide(mode osSandboxMode, harness, level string, ready func() error) (string, error) {
	return osSandboxDecideOn(OSSandboxDefaultOn, mode, harness, level, ready)
}

// osSandboxRequired tells whether a Pi or OpenCode worker of a project must
// run in the wrapper: the project is listed, or nothing was set and this OS
// has the sandbox on by default.
func osSandboxRequired(defaultOn bool, mode osSandboxMode) bool {
	return mode == osSandboxExplicit || (mode == osSandboxDefault && defaultOn)
}

// osSandboxDecideOn is osSandboxDecide for an OS whose default is defaultOn.
// Where the default is off (a Mac before 2026-11-17), a Pi or OpenCode worker
// of a project that is not listed runs without the wrapper, and says so; the
// marker it reports is kept for records of that time.
func osSandboxDecideOn(defaultOn bool, mode osSandboxMode, harness, level string, ready func() error) (string, error) {
	if mode == osSandboxOff {
		return "", nil
	}
	if mode == osSandboxDefault && !defaultOn {
		if harness == "pi" || harness == "opencode" {
			return protocol.OSSandboxNoneMacOS, nil
		}
		return "", nil
	}
	where := "the OS sandbox is on by default for Pi and OpenCode workers"
	if mode == osSandboxExplicit {
		where = "this project has the OS sandbox on (workers.os_sandbox in node.json on this machine)"
	}
	const off = `or turn it off for this project with "os_sandbox_off": ["<project>"] under "workers" in node.json on this machine (then the worker runs without an OS sandbox)`
	switch harness {
	case "codex", "agy":
		return "", nil // their own sandbox, inside
	case "claude":
		if mode == osSandboxDefault {
			return "", nil // Claude Code's own sandbox settings, set per worker
		}
		return "", fmt.Errorf("%s, and it cannot wrap a Claude worker: `claude --bg` runs the session in Claude Code's own background service, outside any wrapper. Use a Pi, OpenCode or Codex worker, or take the project out of workers.os_sandbox", where)
	case "pi", "opencode":
	default:
		if mode == osSandboxDefault {
			return "", nil
		}
		return "", fmt.Errorf("%s, and it cannot wrap %s workers; or take the project out of workers.os_sandbox", where, harness)
	}
	if err := ready(); err != nil {
		return "", fmt.Errorf("%s, but it cannot run here: %v. The worker was not started without it; %s", where, err, off)
	}
	return osSandboxName, nil
}

// OSSandboxOffEverywhere tells whether node.json turns the OS sandbox off for
// every project.
func OSSandboxOffEverywhere(s WorkerSettings) bool { return containsString(s.OSSandboxOff, "*") }

// OSSandboxCheck is the check that setup runs: the wrapper's name when a Pi or
// OpenCode worker can run in the OS sandbox on this machine, else the reason
// and the fix.
func OSSandboxCheck() (string, error) {
	if err := osSandboxReady(); err != nil {
		return "", err
	}
	return osSandboxName, nil
}

// osSandboxChoice applies osSandboxDecide with this daemon's settings.
// recorded is true for a worker that already ran in the wrapper. A Claude
// worker has no wrapper of its own; its mark says when Claude Code's sandbox
// lacks a tool here (claude_sandbox.go).
func (d *Daemon) osSandboxChoice(project, harness, level string, recorded bool) (string, error) {
	ready := osSandboxReady
	if d.cfg.testOSSandboxReady != nil {
		ready = d.cfg.testOSSandboxReady
	}
	mode := d.cfg.Workers.osSandboxMode(project)
	if recorded {
		mode = osSandboxExplicit
	}
	name, err := osSandboxDecide(mode, harness, level, ready)
	if err != nil || name != "" || harness != "claude" {
		return name, err
	}
	return d.claudeSandboxState(), nil
}

// readOSSandboxMarker returns the wrapper recorded in a worker's folder.
func readOSSandboxMarker(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, osSandboxMarker))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// mountPair binds Src at Dst.
type mountPair struct{ Src, Dst string }

// osSandboxPolicy is what the wrapper of one worker allows (decision 0197):
// writes only in Writable, everything else open. Paths are absolute and
// resolved.
type osSandboxPolicy struct {
	Writable []string    // bound writable at the same path
	ReadOnly []mountPair // owner files bound read-only into the private state
	Dir      string      // the working folder
}

// bwrapArgs is the bubblewrap command line that runs bin with args under p.
// The host network is shared, so loopback ports and every unix socket work.
//
// Not used on purpose: --die-with-parent (a worker outlives the daemon, which
// finds it again after a restart; the flag would kill it on every restart),
// --new-session (the daemon already starts bwrap in a session of its own with
// no terminal; a second session would take the harness out of the process
// group that the daemon stops) and --as-pid-1 (a harness as PID 1 of the
// namespace would ignore the daemon's SIGTERM).
func bwrapArgs(p osSandboxPolicy, bin string, args []string) []string {
	a := []string{"--unshare-all", "--share-net", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc"}
	for _, w := range p.Writable {
		a = append(a, "--bind", w, w)
	}
	for _, r := range p.ReadOnly {
		a = append(a, "--ro-bind", r.Src, r.Dst)
	}
	if p.Dir != "" {
		a = append(a, "--chdir", p.Dir)
	}
	a = append(a, "--", bin)
	return append(a, args...)
}

// osSandboxInput is what the daemon found out about one worker start.
type osSandboxInput struct {
	WorkerDir string      // the worker's private folder
	Path      string      // the working folder (the worktree)
	Roots     []string    // the folders every worker may write (workerWriteRoots)
	State     []string    // the harness's writable private state
	StateRO   []mountPair // owner files bound read-only into the private state
	SocketDir string      // the folder with the agent-net socket
}

// buildOSSandboxPolicy turns the facts of a start into the wrapper's policy.
// The socket folder is bound as a folder, not the socket file: the daemon
// makes a new socket at every restart, and a file bind would keep the dead one.
func buildOSSandboxPolicy(in osSandboxInput) osSandboxPolicy {
	p := osSandboxPolicy{Dir: in.Path, ReadOnly: in.StateRO}
	for _, w := range append(append(append([]string{in.Path, osSandboxTmp(in.WorkerDir)}, in.Roots...), in.State...), in.SocketDir) {
		if w != "" && !containsString(p.Writable, w) {
			p.Writable = append(p.Writable, w)
		}
	}
	return p
}

// osSandboxTmp is the worker's own temp folder: TMPDIR inside the wrapper.
func osSandboxTmp(workerDir string) string { return filepath.Join(workerDir, "tmp") }

// underAny tells whether path is one of dirs or below one of them.
func underAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if path == d || strings.HasPrefix(path, strings.TrimSuffix(d, "/")+"/") {
			return true
		}
	}
	return false
}

// resolved is the absolute path with symlinks resolved, or the cleaned path
// when it cannot be resolved.
func resolved(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// osSandboxWrap prepares the private folders of a worker that runs in the
// wrapper and returns the command to run in place of bin and args, with the
// environment the harness needs inside. It records the wrapper in the
// worker's folder.
func (d *Daemon) osSandboxWrap(w *worker, bin string, args, env []string) (string, []string, []string, error) {
	d.wmu.Lock()
	harness, name := w.harness, w.osSandbox
	d.wmu.Unlock()
	tool, err := osSandboxTool()
	if err != nil {
		return "", nil, nil, fmt.Errorf("the OS sandbox is on for this worker, but %w", err)
	}
	in, dir, err := d.osSandboxInputFor(w)
	if err != nil {
		return "", nil, nil, err
	}
	set := osSandboxSetEnv(osSandboxTmp(in.WorkerDir))
	switch harness {
	case "pi":
		if err := d.piPrivateAgent(&in, set); err != nil {
			return "", nil, nil, err
		}
	case "opencode":
		for _, sub := range []string{"data", "state"} {
			p := filepath.Join(dir, sub)
			if err := os.MkdirAll(p, 0o700); err != nil {
				return "", nil, nil, err
			}
			in.State = append(in.State, resolved(p))
		}
	default:
		return "", nil, nil, fmt.Errorf("the OS sandbox cannot wrap %s workers", harness)
	}
	if err := os.WriteFile(filepath.Join(dir, osSandboxMarker), []byte(name+"\n"), 0o600); err != nil {
		return "", nil, nil, err
	}
	argv := osSandboxCommand(buildOSSandboxPolicy(in), bin, args)
	return tool, argv, workerEnv(env, set), nil
}

// osSandboxInputFor collects the facts the wrapper's policy needs for a worker:
// its private folder (made, with its tmp), its worktree and the folders every
// worker may write. It also returns the private folder's path.
func (d *Daemon) osSandboxInputFor(w *worker) (osSandboxInput, string, error) {
	d.wmu.Lock()
	path, dir := w.path, w.dir
	d.wmu.Unlock()
	if dir == "" {
		dir = filepath.Join(d.cfg.StateDir, "workers", w.id)
	}
	if err := os.MkdirAll(osSandboxTmp(dir), 0o700); err != nil {
		return osSandboxInput{}, "", err
	}
	in := osSandboxInput{WorkerDir: resolved(dir), Path: resolved(path)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, r := range d.workerWriteRoots(ctx, w) {
		in.Roots = append(in.Roots, resolved(r))
	}
	if fileExists(d.cfg.SocketPath) {
		in.SocketDir = resolved(socketDir(d.cfg.SocketPath))
	}
	return in, dir, nil
}

// piPrivateAgent gives a Pi worker its own agent folder. Pi takes its locks by
// making a folder next to auth.json and settings.json, so the folder must be
// writable; but Pi runs an auth.json key that starts with "!" as a shell
// command and loads code from extensions, npm, git and settings.json, so the
// owner's real folder must not be. The private folder gets a copy of the
// credentials (refreshed tokens stay in it, as for OpenCode workers), and
// every other entry of the owner's folder is bound into it read-only. The
// session folder stays the real one: the daemon reads the worker's usage there.
func (d *Daemon) piPrivateAgent(in *osSandboxInput, set map[string]string) error {
	real := os.Getenv("PI_CODING_AGENT_DIR")
	if real == "" {
		real = filepath.Join(home(), ".pi", "agent")
	}
	real = resolved(real)
	agent := filepath.Join(in.WorkerDir, "pi-agent")
	// A fresh folder at each start: the copy of the credentials is new, and no
	// mount point of an earlier start can clash with an entry of today.
	if err := os.RemoveAll(agent); err != nil {
		return err
	}
	if err := os.MkdirAll(agent, 0o700); err != nil {
		return err
	}
	sessions := resolved(d.cfg.PiSessionsDir)
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(real)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		src := filepath.Join(real, name)
		switch {
		case name == "auth.json" || name == "models-store.json":
			if err := copyPrivate(src, filepath.Join(agent, name)); err != nil {
				return fmt.Errorf("copy Pi %s: %w", name, err)
			}
		case name == "sessions" || strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".log"):
			// sessions: bound below; locks and logs: the worker's own
		default:
			in.StateRO = append(in.StateRO, mountPair{resolved(src), filepath.Join(agent, name)})
		}
	}
	in.State = append(in.State, agent, sessions)
	set["PI_CODING_AGENT_DIR"] = agent
	set["PI_CODING_AGENT_SESSION_DIR"] = sessions
	return nil
}

// copyPrivate copies a regular file with mode 0600.
func copyPrivate(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// osSandboxSetEnv is the environment the wrapper gives a command inside it:
// every temp folder variable names the worker's own temp folder, which the
// daemon removes with the worker. Go writes its build files there too, and not
// to the Go temp folder on /dev/shm, which the wrapper's own /dev does not have.
func osSandboxSetEnv(tmp string) map[string]string {
	set := map[string]string{}
	for _, k := range []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR"} {
		set[k] = tmp
	}
	return set
}
