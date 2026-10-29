// Package harnesses finds which agent harnesses are installed on this machine.
//
// A daemon started by launchd (macOS) or a systemd user unit has a short PATH,
// so the search also looks in the usual install folders and, only when a
// harness is still missing, in the PATH of the user's login shell.
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
	// Dirs are searched after PATH. Default: DefaultDirs().
	Dirs []string
	// LoginShellPATH returns the PATH of the user's login shell, or "". It is
	// only called when a harness was not found in PATH and Dirs.
	// Default: run $SHELL as a login shell.
	LoginShellPATH func(ctx context.Context) string
	// Run runs a program and returns its output. Default: exec with the
	// search path in PATH, so scripts that start with "#!/usr/bin/env node" work.
	Run func(ctx context.Context, path string, args ...string) (string, error)
}

// DefaultDirs are the usual places where these tools are installed.
func DefaultDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin", "/bin"}
	if home != "" {
		dirs = append([]string{
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".npm-global", "bin"),
			filepath.Join(home, ".opencode", "bin"),
		}, dirs...)
	}
	return dirs
}

// Detect reports the installed harnesses, in the order of Names. A harness that
// is not found is left out. The result is never nil.
func Detect(ctx context.Context, o Options) []protocol.HarnessInfo {
	if o.PATH == "" {
		o.PATH = os.Getenv("PATH")
	}
	if o.Dirs == nil {
		o.Dirs = DefaultDirs()
	}
	if o.LoginShellPATH == nil {
		o.LoginShellPATH = loginShellPATH
	}
	search := splitPath(o.PATH)
	search = append(search, o.Dirs...)
	if o.Run == nil {
		o.Run = func(ctx context.Context, path string, args ...string) (string, error) {
			return runProgram(ctx, path, strings.Join(search, string(os.PathListSeparator)), args...)
		}
	}

	paths := map[string]string{}
	missing := false
	for _, n := range Names {
		if p := find(n, search); p != "" {
			paths[n] = p
		} else {
			missing = true
		}
	}
	if missing {
		if sp := o.LoginShellPATH(ctx); sp != "" {
			shellDirs := splitPath(sp)
			search = append(search, shellDirs...)
			for _, n := range Names {
				if paths[n] == "" {
					if p := find(n, shellDirs); p != "" {
						paths[n] = p
					}
				}
			}
		}
	}

	out := []protocol.HarnessInfo{}
	for _, n := range Names {
		p := paths[n]
		if p == "" {
			continue
		}
		h := protocol.HarnessInfo{Name: n, Path: p}
		vctx, cancel := context.WithTimeout(ctx, VersionTimeout)
		if v, err := o.Run(vctx, p, "--version"); err == nil {
			h.Version = firstLine(v)
		}
		cancel()
		if n == "opencode" {
			// `opencode models` prints one provider/model per line.
			mctx, cancel := context.WithTimeout(ctx, ModelsTimeout)
			if m, err := o.Run(mctx, p, "models"); err == nil {
				h.Models = ParseModels(m)
			}
			cancel()
		}
		out = append(out, h)
	}
	return out
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
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0 {
			return p
		}
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
