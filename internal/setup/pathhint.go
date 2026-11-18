package setup

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// pathStep tells a person at the terminal when the folder of agent-net is not
// on the PATH of their shell, and prints the line that puts it there. Setup
// does not edit shell files itself.
func (r *runner) pathStep() {
	if !r.interactive {
		return
	}
	file, line, missing := pathHint(r.Exe, os.Getenv("PATH"), home(), os.Getenv("SHELL"), runtime.GOOS)
	if !missing {
		return
	}
	r.hint("agent-net is not on the PATH of this shell. Add this line to %s, then open a new terminal:", file)
	r.hint("  %s", line)
}

// pathHint returns the shell startup file and the export line for the folder
// of exe, and whether that folder is missing from pathEnv. The file follows the
// shell: zsh reads ~/.zshrc, and bash reads ~/.bash_profile on macOS and
// ~/.bashrc elsewhere. Other shells get ~/.profile.
func pathHint(exe, pathEnv, home, shell, goos string) (file, line string, missing bool) {
	dir := filepath.Clean(filepath.Dir(exe))
	for _, entry := range filepath.SplitList(pathEnv) {
		if entry != "" && filepath.Clean(entry) == dir {
			return "", "", false
		}
	}
	switch name := filepath.Base(shell); {
	case name == "zsh":
		file = filepath.Join(home, ".zshrc")
	case name == "bash" && goos == "darwin":
		file = filepath.Join(home, ".bash_profile")
	case name == "bash":
		file = filepath.Join(home, ".bashrc")
	default:
		file = filepath.Join(home, ".profile")
	}
	shown := dir
	if rest, ok := strings.CutPrefix(dir, filepath.Clean(home)+string(filepath.Separator)); ok {
		shown = "$HOME/" + rest
	}
	return tildePath(file, home), `export PATH="` + shown + `:$PATH"`, true
}

// tildePath writes a path under home as ~/rest.
func tildePath(p, home string) string {
	if rest, ok := strings.CutPrefix(p, filepath.Clean(home)+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return p
}
