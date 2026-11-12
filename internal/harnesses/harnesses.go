// Package harnesses finds which agent harnesses are installed on this machine.
//
// A daemon started by launchd (macOS) or a systemd user unit has a short PATH,
// so the search also looks in the usual install folders (including the folders
// of Node version managers) and in the PATH of the user's login shell. It never
// starts an interactive shell.
package harnesses

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Names are the harnesses we look for, in the order they are reported.
var Names = []string{"claude", "opencode", "pi", "codex", "agy"}

// Limits.
const (
	VersionTimeout = 5 * time.Second
	ModelsTimeout  = 20 * time.Second
	ShellTimeout   = 3 * time.Second
	MaxModels      = 2000
	maxVersionLen  = 80
	maxModelLen    = 200
)

// Options changes how Detect works. The zero value is the real thing; tests
// set the fields.
type Options struct {
	// PATH is the search path. Default: the PATH of this process.
	PATH string
	// Dirs are searched after PATH. Default: DefaultDirs(Home).
	Dirs []string
	// Home is the home folder used for the default Dirs. Default: the user's.
	Home string
	// LoginShellPATH returns the PATH of the user's login shell, or "". It is
	// always called: the copy that comes first there is the primary one.
	// Default: run $SHELL as a login shell.
	LoginShellPATH func(ctx context.Context) string
	// Run runs a program and returns its output. Default: exec with the
	// search path in PATH, so scripts that start with "#!/usr/bin/env node" work.
	Run func(ctx context.Context, path string, args ...string) (string, error)
}

// Detect reports the installed harnesses, in the order of Names. A harness that
// is not found is left out. The result is never nil.
//
// A harness can be installed more than once (for example OpenCode v1 from its
// install script and v2 from npm). Detect finds every copy, drops copies that
// are the same file (same real path), and reports one entry per harness: the
// primary copy in the main fields and the others in Others. The primary copy is
// the first one in the login shell's PATH (Primary is then true). When the
// login shell does not find the harness, it is the first copy in the search
// order and Primary is false.
func Detect(ctx context.Context, o Options) []protocol.HarnessInfo {
	s := prepare(ctx, &o)

	out := []protocol.HarnessInfo{}
	for _, n := range Names {
		installs := findAll(n, s.dirs)
		if len(installs) == 0 {
			continue
		}
		primary := false
		if first := find(n, s.loginDirs); first != "" {
			real := realPath(first)
			for i, p := range installs {
				if realPath(p) == real {
					// Move the login shell's copy to the front, under the
					// path the login shell finds it at.
					copy(installs[1:i+1], installs[:i])
					installs[0] = first
					primary = true
					break
				}
			}
		}
		h := protocol.HarnessInfo{Name: n, Path: installs[0], Primary: primary}
		h.Version = version(ctx, o, installs[0])
		h.SignedIn, h.SignIn = signIn(ctx, o, n, installs[0])
		if n == "codex" {
			mctx, cancel := context.WithTimeout(ctx, ModelsTimeout)
			h.Models, h.ModelDefaultEfforts, _ = codexModels(mctx, installs[0], s.path)
			cancel()
		} else if n == "agy" {
			// `agy models` needs the network and a sign-in, so the same probe
			// says whether the machine is signed in (decision 0049 D4).
			mctx, cancel := context.WithTimeout(ctx, ModelsTimeout)
			h.Models, h.SignedIn, _ = agyModels(mctx, installs[0], s.path)
			cancel()
		} else {
			h.Models = models(ctx, o, n, installs[0], s.dirs)
		}
		for _, p := range installs[1:] {
			h.Others = append(h.Others, protocol.HarnessInstall{Path: p, Version: version(ctx, o, p)})
		}
		out = append(out, h)
	}
	return out
}

// Locate finds an installed copy of one harness the same way Detect does, and
// returns it when it runs. It is the fallback for a start that arrives before
// detection has finished, or after a detection run missed the harness.
func Locate(ctx context.Context, name string, o Options) string {
	s := prepare(ctx, &o)
	installs := findAll(name, s.dirs)
	if len(installs) == 0 {
		return ""
	}
	if first := find(name, s.loginDirs); first != "" {
		real := realPath(first)
		for i, p := range installs {
			if realPath(p) == real {
				copy(installs[1:i+1], installs[:i])
				installs[0] = first
				break
			}
		}
	}
	for _, p := range installs {
		if runs(ctx, o, p) {
			return p
		}
	}
	return ""
}

// Executable finds a program on the harness search path without running it.
// The login shell's copy takes precedence, as it does for harnesses.
func Executable(ctx context.Context, name string, o Options) string {
	s := prepare(ctx, &o)
	if p := find(name, s.loginDirs); p != "" {
		return p
	}
	return find(name, s.dirs)
}

// RunUpdate runs the selected harness in the same search environment as the
// probes. Input is closed and the process group is stopped on cancellation.
func RunUpdate(ctx context.Context, path string, o Options, args ...string) (string, error) {
	s := prepare(ctx, &o)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(path)+string(os.PathListSeparator)+s.path, "CI=1", "TERM=dumb", "GIT_TERMINAL_PROMPT=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 500 * time.Millisecond
	var out updateTail
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return string(out), err
}

// updateTail bounds output from an updater while retaining its final line.
type updateTail []byte

func (b *updateTail) Write(p []byte) (int, error) {
	n := len(p)
	const limit = 8192
	if len(p) >= limit {
		*b = append((*b)[:0], p[len(p)-limit:]...)
		return n, nil
	}
	if over := len(*b) + len(p) - limit; over > 0 {
		*b = append((*b)[:0], (*b)[over:]...)
	}
	*b = append(*b, p...)
	return n, nil
}

// search is where Detect and Locate look, and the PATH a harness sees.
type search struct {
	dirs      []string
	loginDirs []string
	path      string
}

// prepare fills the defaults of o and builds the search order: PATH, the known
// install folders, then the login shell's PATH.
func prepare(ctx context.Context, o *Options) search {
	if o.PATH == "" {
		o.PATH = os.Getenv("PATH")
	}
	if o.Dirs == nil {
		o.Dirs = DefaultDirs(o.Home)
	}
	if o.LoginShellPATH == nil {
		o.LoginShellPATH = loginShellPATH
	}
	var loginDirs []string
	if sp := o.LoginShellPATH(ctx); sp != "" {
		loginDirs = splitPath(sp)
	}
	dirs := splitPath(o.PATH)
	dirs = append(dirs, o.Dirs...)
	dirs = append(dirs, loginDirs...)
	s := search{dirs: dirs, loginDirs: loginDirs, path: strings.Join(dirs, string(os.PathListSeparator))}
	if o.Run == nil {
		o.Run = func(ctx context.Context, path string, args ...string) (string, error) {
			return runProgram(ctx, path, s.path, args...)
		}
	}
	return s
}

// runs proves that a copy starts, with the same bounded version probe Detect
// uses.
func runs(ctx context.Context, o Options, path string) bool {
	vctx, cancel := context.WithTimeout(ctx, VersionTimeout)
	defer cancel()
	_, err := o.Run(vctx, path, "--version")
	return err == nil
}

// models returns the model list of a harness, or nil when it cannot be read.
// The whole read is bounded by ModelsTimeout.
func models(ctx context.Context, o Options, name, path string, search []string) []string {
	switch name {
	case "opencode":
		mctx, cancel := context.WithTimeout(ctx, ModelsTimeout)
		defer cancel()
		if m, err := o.Run(mctx, path, "models"); err == nil {
			return ParseModels(m)
		}
	case "pi":
		mctx, cancel := context.WithTimeout(ctx, ModelsTimeout)
		defer cancel()
		if m, err := o.Run(mctx, path, "--list-models"); err == nil {
			return ParsePiModels(m)
		}
	case "claude":
		return ClaudeModels()

	}
	return nil
}

func version(ctx context.Context, o Options, path string) string {
	vctx, cancel := context.WithTimeout(ctx, VersionTimeout)
	defer cancel()
	if v, err := o.Run(vctx, path, "--version"); err == nil {
		return firstLine(v)
	}
	return ""
}

// findAll returns every executable called name in dirs, in order, without
// copies that resolve to the same real file.
func findAll(name string, dirs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range dirs {
		p := isExec(filepath.Join(d, name))
		if p == "" {
			continue
		}
		r := realPath(p)
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, p)
	}
	return out
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// ParseModels reads the output of `opencode models`: one "provider/model" per
// line. Other lines are ignored. The result is sorted, without duplicates and
// at most MaxModels long. It is nil when no model is found.
func ParseModels(out string) []string {
	seen := map[string]bool{}
	var models []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, "/")
		if i <= 0 || i == len(line)-1 || len(line) > maxModelLen || strings.ContainsAny(line, " \t") || seen[line] {
			continue
		}
		seen[line] = true
		models = append(models, line)
	}
	sort.Strings(models)
	if len(models) > MaxModels {
		models = models[:MaxModels]
	}
	return models
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > maxVersionLen {
				l = l[:maxVersionLen]
			}
			return l
		}
	}
	return ""
}

func splitPath(p string) []string {
	var out []string
	for _, d := range strings.Split(p, string(os.PathListSeparator)) {
		if d = strings.TrimSpace(d); d != "" && filepath.IsAbs(d) {
			out = append(out, d)
		}
	}
	return out
}

// find returns the first executable regular file called name in dirs.
func find(name string, dirs []string) string {
	for _, d := range dirs {
		if p := isExec(filepath.Join(d, name)); p != "" {
			return p
		}
	}
	return ""
}

func isExec(p string) string {
	if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
		return p
	}
	return ""
}

func runProgram(ctx context.Context, path, searchPATH string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	// The program's own folder first: wrappers find their interpreter there.
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(path)+string(os.PathListSeparator)+searchPATH)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if len(args) == 2 && args[0] == "login" && args[1] == "status" {
		cmd.Stderr = &buf
	}
	// A model list that fails usually says why on stderr ("please run opencode
	// auth login"), and that text belongs in the caller's error.
	if len(args) == 1 && (args[0] == "models" || args[0] == "--list-models") {
		cmd.Stderr = &buf
	}
	cmd.WaitDelay = 500 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

const marker = "__AGENT_NET_PATH__"

// loginShellPATH asks the user's login shell for its PATH. The shell is the
// one in $SHELL (on macOS /bin/zsh when that is not set), started as the user
// with no terminal and no input, and stopped after ShellTimeout. It reads the
// login startup files (not the interactive ones), as a terminal window does.
func loginShellPATH(ctx context.Context) string {
	sh := os.Getenv("SHELL")
	if sh == "" && runtime.GOOS == "darwin" {
		sh = "/bin/zsh"
	}
	switch filepath.Base(sh) {
	case "bash", "zsh", "sh", "dash", "ksh", "fish":
	default:
		return ""
	}
	if !filepath.IsAbs(sh) {
		return ""
	}
	if st, err := os.Stat(sh); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, ShellTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, sh, "-l", "-c", `printf '%s%s%s' `+marker+` "$PATH" `+marker)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.WaitDelay = 500 * time.Millisecond
	if cmd.Run() != nil {
		return ""
	}
	s := buf.String()
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	s = s[i+len(marker):]
	j := strings.Index(s, marker)
	if j < 0 {
		return ""
	}
	return s[:j]
}

func signIn(ctx context.Context, o Options, name, path string) (string, string) {
	if name != "claude" && name != "codex" {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(ctx, VersionTimeout)
	defer cancel()
	args := []string{"auth", "status"}
	if name == "codex" {
		args = []string{"login", "status"}
	}
	out, err := o.Run(ctx, path, args...)
	if ctx.Err() != nil {
		return "", ""
	}
	if name == "codex" {
		if strings.TrimSpace(out) == "Not logged in" {
			return "no", ""
		}
		if err == nil {
			switch strings.TrimSpace(out) {
			case "Logged in using ChatGPT":
				return "yes", "ChatGPT"
			case "Logged in using an API key":
				return "yes", "API key"
			}
		}
		return "", ""
	}
	if err != nil {
		return "", ""
	}
	var status struct {
		LoggedIn   *bool  `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if json.Unmarshal([]byte(out), &status) != nil || status.LoggedIn == nil {
		return "", ""
	}
	if !*status.LoggedIn {
		return "no", ""
	}
	method := ""
	switch status.AuthMethod {
	case "claude.ai", "oauth", "api_key", "apiKey", "console":
		method = status.AuthMethod
	}
	return "yes", method
}
