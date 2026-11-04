package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The OS sandbox of a worker (hardening plan W7, docs/design/worker-os-sandbox.md).
//
// An optional wrapper around the whole harness process of a Pi or OpenCode
// worker, switched on per project in node.json (workers.os_sandbox) on the
// machine that runs the worker. On Linux it is bubblewrap: the host is
// read-only, the worker's folder, the narrow git folders of a commit and the
// harness's private state are writable, /tmp is private, the folders of the
// user's control sockets are hidden, and the agent-net socket is bound back.
//
// Codex and Antigravity keep their own sandboxes and are not wrapped. A Claude
// worker is `claude --bg`, which runs the session in Claude Code's own
// background service, outside any wrapper: with the switch on, it is refused.
// The level (decision 0020) is not chosen here; the wrapper applies it.
//
// Never a silent start without the sandbox: when the switch is on and the
// wrapper cannot be used, the start is refused with the reason.

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

// osSandboxOn tells whether node.json switches the OS sandbox on for project.
func (s WorkerSettings) osSandboxOn(project string) bool {
	for _, p := range s.OSSandbox {
		if p == "*" || strings.EqualFold(p, project) {
			return true
		}
	}
	return false
}

// osSandboxDecide is the rule for one start: the wrapper's name, "" when the
// worker runs without it, or the refusal. on is the switch (or a worker that
// was started in the wrapper before); ready checks the tool on this machine.
func osSandboxDecide(on bool, harness, level string, ready func() error) (string, error) {
	if !on {
		return "", nil
	}
	const where = "this project has the OS sandbox on (workers.os_sandbox in node.json on this machine)"
	const off = "or take the project out of workers.os_sandbox"
	switch harness {
	case "codex", "agy":
		return "", nil // their own sandbox, inside
	case "claude":
		return "", fmt.Errorf("%s, and it cannot wrap a Claude worker: `claude --bg` runs the session in Claude Code's own background service, outside any wrapper. Use a Pi, OpenCode or Codex worker, %s", where, off)
	case "pi", "opencode":
	default:
		return "", fmt.Errorf("%s, and it cannot wrap %s workers; %s", where, harness, off)
	}
	if protocol.WorkerPermissionsOrDefault(level) == protocol.WorkersNoNetwork {
		return "", fmt.Errorf("%s, and the workers level is no-network: the OS sandbox wraps the whole %s process, so no-network would also cut its connection to its model. Use a Codex worker for no-network, %s", where, harness, off)
	}
	if err := ready(); err != nil {
		return "", fmt.Errorf("%s, but it cannot run here: %v. The worker was not started without it; %s", where, err, off)
	}
	return osSandboxName, nil
}

// osSandboxChoice applies osSandboxDecide with this daemon's switch. recorded
// is true for a worker that already ran in the wrapper.
func (d *Daemon) osSandboxChoice(project, harness, level string, recorded bool) (string, error) {
	ready := osSandboxReady
	if d.cfg.testOSSandboxReady != nil {
		ready = d.cfg.testOSSandboxReady
	}
	return osSandboxDecide(recorded || d.cfg.Workers.osSandboxOn(project), harness, level, ready)
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

// osSandboxPolicy is what the wrapper of one worker allows. Paths are absolute
// and resolved.
type osSandboxPolicy struct {
	Network  bool        // keep the host network
	Hide     []string    // folders hidden by an empty tmpfs
	Tmp      string      // private folder bound at /tmp
	Mask     []string    // files hidden by /dev/null
	Visible  []string    // bound read-only at the same path, for paths a hidden folder may cover
	Writable []string    // bound writable at the same path
	ReadOnly []mountPair // bound read-only on top of the writable paths
	Socket   string      // the agent-net socket, bound at its own path
	Dir      string      // the working folder
}

// bwrapArgs is the bubblewrap command line that runs bin with args under p.
//
// Not used on purpose: --die-with-parent (a worker outlives the daemon, which
// finds it again after a restart; the flag would kill it on every restart),
// --new-session (the daemon already starts bwrap in a session of its own with
// no terminal; a second session would take the harness out of the process
// group that the daemon stops) and --as-pid-1 (a harness as PID 1 of the
// namespace would ignore the daemon's SIGTERM).
func bwrapArgs(p osSandboxPolicy, bin string, args []string) []string {
	a := []string{"--unshare-all"}
	if p.Network {
		a = append(a, "--share-net")
	}
	a = append(a, "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc")
	for _, h := range p.Hide {
		a = append(a, "--tmpfs", h)
	}
	if p.Tmp != "" {
		a = append(a, "--bind", p.Tmp, "/tmp")
	}
	for _, m := range p.Mask {
		a = append(a, "--ro-bind", "/dev/null", m)
	}
	for _, v := range p.Visible {
		a = append(a, "--ro-bind", v, v)
	}
	for _, w := range p.Writable {
		a = append(a, "--bind", w, w)
	}
	for _, r := range p.ReadOnly {
		a = append(a, "--ro-bind", r.Src, r.Dst)
	}
	if p.Socket != "" {
		a = append(a, "--bind", p.Socket, p.Socket)
	}
	if p.Dir != "" {
		a = append(a, "--chdir", p.Dir)
	}
	a = append(a, "--", bin)
	return append(a, args...)
}

// osSandboxInput is what the daemon found out about one worker start.
type osSandboxInput struct {
	Level      string
	WorkerDir  string      // the worker's private folder
	Path       string      // the working folder (the worktree)
	DotGit     string      // the .git entry of the working folder, when there is one
	GitCommon  string      // the main repository's git folder
	GitRoots   []string    // the narrow git folders a commit writes to
	State      []string    // the harness's writable private state
	StateRO    []mountPair // owner files bound read-only into the private state
	Socket     string      // the agent-net socket
	RuntimeDir string      // $XDG_RUNTIME_DIR, hidden
	Masks      []string    // sockets hidden by /dev/null
	Bin        string      // the resolved harness binary
}

// buildOSSandboxPolicy turns the facts of a start into the wrapper's policy.
func buildOSSandboxPolicy(in osSandboxInput) osSandboxPolicy {
	p := osSandboxPolicy{
		Network: protocol.WorkerPermissionsOrDefault(in.Level) != protocol.WorkersNoNetwork,
		Tmp:     filepath.Join(in.WorkerDir, "tmp"),
		Mask:    in.Masks,
		Socket:  in.Socket,
		Dir:     in.Path,
	}
	hidden := []string{"/tmp"}
	if in.RuntimeDir != "" {
		p.Hide = append(p.Hide, in.RuntimeDir)
		hidden = append(hidden, in.RuntimeDir)
	}
	// The worker's folder holds what the daemon gives the harness to read (the
	// commit hooks, OpenCode's session file); the main git folder holds the
	// repository's config. Both may sit in a hidden folder, so they are bound
	// read-only again. So is the harness binary when it is there.
	p.Visible = append(p.Visible, in.WorkerDir)
	if in.GitCommon != "" {
		p.Visible = append(p.Visible, in.GitCommon)
	}
	if in.Bin != "" && underAny(in.Bin, hidden) {
		p.Visible = append(p.Visible, in.Bin)
	}
	if protocol.WorkerPermissionsOrDefault(in.Level) != protocol.WorkersReadOnly {
		p.Writable = append(p.Writable, in.Path)
		p.Writable = append(p.Writable, in.GitRoots...)
	} else {
		// The real tree, read-only: in a hidden folder the worker would
		// otherwise get an empty, writable stand-in.
		p.Visible = append(p.Visible, in.Path)
	}
	p.Writable = append(p.Writable, in.State...)
	// The .git file or folder of the working folder stays read-only, so the
	// worker cannot point git at a folder it controls or add a hook.
	if in.DotGit != "" {
		p.ReadOnly = append(p.ReadOnly, mountPair{in.DotGit, in.DotGit})
	}
	p.ReadOnly = append(p.ReadOnly, in.StateRO...)
	return p
}

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
	harness, level, path, dir, name := w.harness, w.permissions, w.path, w.dir, w.osSandbox
	d.wmu.Unlock()
	if dir == "" {
		dir = filepath.Join(d.cfg.StateDir, "workers", w.id)
	}
	tool, err := exec.LookPath("bwrap")
	if err != nil {
		return "", nil, nil, fmt.Errorf("the OS sandbox is on for this worker, but bubblewrap is not installed: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return "", nil, nil, err
	}
	in := osSandboxInput{Level: level, WorkerDir: resolved(dir), Path: resolved(path),
		RuntimeDir: runtimeDirToHide(), Masks: hostSocketMasks()}
	if fileExists(d.cfg.SocketPath) {
		in.Socket = resolved(d.cfg.SocketPath)
	}
	if lp, err := exec.LookPath(bin); err == nil {
		in.Bin = resolved(lp)
	}
	if dg := filepath.Join(in.Path, ".git"); fileExists(dg) {
		in.DotGit = dg
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if common, err := gitOut(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && common != "" {
		in.GitCommon = resolved(common)
		for _, r := range codexGitRoots(ctx, path) {
			in.GitRoots = append(in.GitRoots, resolved(r))
		}
	}
	set := map[string]string{"TMPDIR": "/tmp"}
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
	argv := bwrapArgs(buildOSSandboxPolicy(in), bin, args)
	return tool, argv, workerEnv(env, set), nil
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

// runtimeDirToHide is the user's runtime folder ($XDG_RUNTIME_DIR, else
// /run/user/<uid>): the systemd user bus and the user's agent sockets.
func runtimeDirToHide() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return ""
	}
	return resolved(dir)
}

// hostSocketMasks are the sockets that give root to their users and sit
// outside the hidden folders: the Docker socket.
func hostSocketMasks() []string {
	var out []string
	for _, p := range []string{"/run/docker.sock", "/var/run/docker.sock"} {
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		if r := resolved(p); !containsString(out, r) {
			out = append(out, r)
		}
	}
	return out
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
