package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/niparis/agent-net/internal/harnesses"
)

// FindHerdr returns the herdr binary the daemon would use when the node
// configuration names no path. Setup uses it so a new machine gets herdr in the
// same places the daemon looks: PATH, the usual install folders, ~/.local/bin
// and the login shell's PATH.
func FindHerdr(configured string) (string, error) {
	bin, looked, _ := findHerdrPath(configured, harnesses.Options{})
	if bin == "" {
		return "", fmt.Errorf("herdr not found (looked in: %s; login shell PATH)", strings.Join(looked, string(os.PathListSeparator)))
	}
	return bin, nil
}

// findHerdrPath is the executable search shared by the daemon and setup. It
// returns the path, the places it looked for the error message, and the
// ~/.local/bin path the daemon logs when another copy was selected.
func findHerdrPath(configured string, opts harnesses.Options) (string, []string, string) {
	const name = "herdr"
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
	return bin, looked, local
}

// lookupHerdrBinary uses the harness search, including the login shell's primary
// copy. Record the choice and any local fallback in the daemon log.
func (d *Daemon) lookupHerdrBinary() (string, error) {
	const name = "herdr"
	d.binaryMu.Lock()
	defer d.binaryMu.Unlock()
	if bin := d.binaryPaths[name]; bin != "" && isExecutable(bin) {
		return bin, nil
	}
	configured := d.cfg.HarnessPaths[name]
	var opts harnesses.Options
	if d.cfg.HarnessSearch != nil {
		opts = *d.cfg.HarnessSearch
	}
	bin, looked, local := findHerdrPath(configured, opts)
	if bin == "" {
		return "", fmt.Errorf("%s not found (looked in: %s; login shell PATH)", name, strings.Join(looked, string(os.PathListSeparator)))
	}
	if d.binaryPaths == nil {
		d.binaryPaths = make(map[string]string)
	}
	d.binaryPaths[name] = bin
	if d.binaryLogged == nil {
		d.binaryLogged = make(map[string]string)
	}
	changed := d.binaryLogged[name] != bin
	d.binaryLogged[name] = bin
	if changed && d.log != nil {
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

// invalidateBinary makes the next lookup refresh a failed executable. Keep the
// logged choice so rediscovering the same path does not repeat the log message.
func (d *Daemon) invalidateBinary(name, path string) {
	d.binaryMu.Lock()
	defer d.binaryMu.Unlock()
	if d.binaryPaths[name] == path {
		delete(d.binaryPaths, name)
	}
}
