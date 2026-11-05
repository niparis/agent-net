package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/harnesses"
)

// lookupHerdrBinary uses the harness search, including the login shell's primary
// copy. Record the choice and any local fallback in the daemon log.
func (d *Daemon) lookupHerdrBinary() (string, error) {
	const name = "herdr"
	configured := d.cfg.HarnessPaths[name]
	var opts harnesses.Options
	if d.cfg.HarnessSearch != nil {
		opts = *d.cfg.HarnessSearch
	}
	if opts.PATH != "" && opts.PATH != os.Getenv("PATH") {
		opts.PATH += string(os.PathListSeparator) + os.Getenv("PATH")
	}
	dirs := opts.Dirs
	if dirs == nil {
		dirs = harnesses.DefaultDirs(opts.Home)
	}
	home := opts.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	local := filepath.Join(home, ".local", "bin", name)
	opts.Dirs = append(append([]string{}, dirs...), filepath.Dir(local))
	// Capture the search directories while reusing Executable's login
	// shell lookup. The PATH fallback and default folders remain visible too.
	looked := []string{opts.PATH}
	if opts.PATH == "" {
		looked[0] = os.Getenv("PATH")
	}
	looked = append(looked, opts.Dirs...)
	login := opts.LoginShellPATH
	if login != nil {
		opts.LoginShellPATH = func(ctx context.Context) string {
			path := login(ctx)
			looked = append(looked, path)
			return path
		}
	}
	bin := configured
	if !filepath.IsAbs(bin) || !isExecutable(bin) {
		bin = harnesses.Executable(context.Background(), name, opts)
	}
	if bin == "" {
		return "", fmt.Errorf("%s not found (looked in: %s; login shell PATH)", name, strings.Join(looked, string(os.PathListSeparator)))
	}
	if d.log != nil {
		d.Logf("%s binary: %s (harness search, login shell preferred)", name, bin)
		if isExecutable(local) {
			selected, _ := filepath.EvalSymlinks(bin)
			other, _ := filepath.EvalSymlinks(local)
			if selected != other {
				d.Logf("%s other installation: %s (using %s)", name, local, bin)
			}
		}
	}
	return bin, nil
}
