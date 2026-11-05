package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestHerdrTmuxLoginShellLookup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	login := t.TempDir()
	stale := fakeHarnessScript(t, filepath.Join(home, ".local", "bin"), "herdr")
	herdr := fakeHarnessScript(t, login, "herdr")
	tmux := fakeHarnessScript(t, login, "tmux")
	// Exercise the real harness login-shell parser without reading startup files.
	shell := filepath.Join(t.TempDir(), "zsh")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf '__AGENT_NET_PATH__%s__AGENT_NET_PATH__' '"+login+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	logs := &logBuf{}
	d := &Daemon{cfg: Config{HarnessSearch: &harnesses.Options{Home: home, Dirs: []string{filepath.Dir(stale)}}}, log: log.New(logs, "", 0)}
	if got := d.herdrBinary(""); got != herdr {
		t.Fatalf("herdr = %q, want login copy %q", got, herdr)
	}
	if got, err := d.terminalBinary("tmux"); err != nil || got != tmux {
		t.Fatalf("tmux = %q, %v", got, err)
	}
	if len(logs.lines(herdr)) == 0 || len(logs.lines(stale)) == 0 {
		t.Fatalf("missing selected/other paths: %s", logs.lines())
	}
}

func TestTmuxMissingSearchError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	d := &Daemon{cfg: Config{HarnessSearch: &harnesses.Options{PATH: dir, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }}}}
	_, err := d.terminalBinary("tmux")
	if err == nil || !strings.Contains(err.Error(), "tmux not found (looked in: "+dir) {
		t.Fatalf("error = %v", err)
	}
}

func TestHerdrFailureNamesBinaryAndError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'fake server refused' >&2\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{cfg: Config{HarnessPaths: map[string]string{"herdr": bin}}}
	plan := d.openInHerdr("", "test", "/tmp/fake-herdr.sock", "", protocol.EnterPlan{Argv: []string{"true"}})
	if !strings.Contains(plan.Note, "herdr at "+bin+" failed:") || !strings.Contains(plan.Note, "exit status 7") {
		t.Fatalf("note = %q", plan.Note)
	}
}
