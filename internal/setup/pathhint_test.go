package setup

import (
	"bytes"
	"testing"
)

// The setup run prints the line for the shell when the folder of agent-net is
// not on PATH. zsh reads ~/.zshrc in a new terminal; ~/.profile is not read.
func TestPathHint(t *testing.T) {
	home := "/home/me"
	cases := []struct {
		name, exe, path, shell, goos string
		wantFile, wantLine           string
		wantMissing                  bool
	}{
		{"on PATH", "/home/me/.local/bin/agent-net", "/usr/bin:/home/me/.local/bin", "/bin/zsh", "darwin", "", "", false},
		{"on PATH with slash", "/home/me/.local/bin/agent-net", "/usr/bin:/home/me/.local/bin/", "/bin/zsh", "darwin", "", "", false},
		{"zsh", "/home/me/.local/bin/agent-net", "/usr/bin", "/bin/zsh", "darwin", "~/.zshrc", `export PATH="$HOME/.local/bin:$PATH"`, true},
		{"bash on macOS", "/home/me/.local/bin/agent-net", "/usr/bin", "/bin/bash", "darwin", "~/.bash_profile", `export PATH="$HOME/.local/bin:$PATH"`, true},
		{"bash on Linux", "/home/me/.local/bin/agent-net", "/usr/bin", "/bin/bash", "linux", "~/.bashrc", `export PATH="$HOME/.local/bin:$PATH"`, true},
		{"other shell", "/home/me/.local/bin/agent-net", "/usr/bin", "/usr/bin/fish", "linux", "~/.profile", `export PATH="$HOME/.local/bin:$PATH"`, true},
		{"no shell set", "/home/me/.local/bin/agent-net", "/usr/bin", "", "linux", "~/.profile", `export PATH="$HOME/.local/bin:$PATH"`, true},
		{"outside home", "/opt/agent-net/agent-net", "/usr/bin", "/bin/zsh", "darwin", "~/.zshrc", `export PATH="/opt/agent-net:$PATH"`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file, line, missing := pathHint(tc.exe, tc.path, home, tc.shell, tc.goos)
			if missing != tc.wantMissing || file != tc.wantFile || line != tc.wantLine {
				t.Fatalf("pathHint = %q, %q, %v; want %q, %q, %v", file, line, missing, tc.wantFile, tc.wantLine, tc.wantMissing)
			}
		})
	}
}

// The step prints nothing when no person is at the terminal, and two lines
// with the file and the line when the folder is missing from PATH.
func TestPathStepPrintsOnlyForAPersonAtTheTerminal(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/usr/bin")
	var out bytes.Buffer
	r := &runner{Options: Options{Out: &out, Exe: "/home/me/.local/bin/agent-net"}}
	r.pathStep()
	if out.Len() != 0 {
		t.Fatalf("a run without a person printed: %q", out.String())
	}
	r.interactive = true
	r.pathStep()
	got := out.String()
	for _, want := range []string{"agent-net is not on the PATH of this shell", "~/.zshrc", `export PATH="$HOME/.local/bin:$PATH"`} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("step output lacks %q:\n%s", want, got)
		}
	}
}
