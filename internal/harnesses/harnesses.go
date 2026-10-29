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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Names are the harnesses we look for, in the order they are reported.
var Names = []string{"claude", "opencode", "pi", "codex"}

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
	search := splitPath(o.PATH)
	search = append(search, o.Dirs...)
	search = append(search, loginDirs...)
	if o.Run == nil {
		o.Run = func(ctx context.Context, path string, args ...string) (string, error) {
			return runProgram(ctx, path, strings.Join(search, string(os.PathListSeparator)), args...)
		}
	}

	out := []protocol.HarnessInfo{}
	for _, n := range Names {
		installs := findAll(n, search)
		if len(installs) == 0 {
			continue
		}
		primary := false
		if first := find(n, loginDirs); first != "" {
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
		if n == "opencode" {
			// `opencode models` prints one provider/model per line.
			mctx, cancel := context.WithTimeout(ctx, ModelsTimeout)
			if m, err := o.Run(mctx, installs[0], "models"); err == nil {
				h.Models = ParseModels(m)
			}
			cancel()
		}
		for _, p := range installs[1:] {
			h.Others = append(h.Others, protocol.HarnessInstall{Path: p, Version: version(ctx, o, p)})
		}
		out = append(out, h)
	}
	return out
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
	cmd.WaitDelay = 500 * time.Millisecond
	if err := cmd.Run(); err != nil {
		return "", err
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
