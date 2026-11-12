package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/harnesses"
	"github.com/niparis/agent-net/internal/protocol"
)

func TestHerdrLoginShellLookup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	login := t.TempDir()
	stale := fakeHarnessScript(t, filepath.Join(home, ".local", "bin"), "herdr")
	herdr := fakeHarnessScript(t, login, "herdr")
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
	if len(logs.lines(herdr)) == 0 || len(logs.lines(stale)) == 0 {
		t.Fatalf("missing selected/other paths: %s", logs.lines())
	}
}

func TestHerdrMissingSearchError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	d := &Daemon{cfg: Config{HarnessSearch: &harnesses.Options{PATH: dir, Dirs: []string{}, LoginShellPATH: func(context.Context) string { return "" }}}}
	_, err := d.lookupHerdrBinary()
	if err == nil || !strings.Contains(err.Error(), "herdr not found (looked in: "+dir) {
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
	plan := d.openInHerdr("", "test", protocol.EnterPlan{Argv: []string{"true"}})
	if !strings.Contains(plan.Note, "herdr at "+bin+" failed:") || !strings.Contains(plan.Note, "exit status 7") {
		t.Fatalf("note = %q", plan.Note)
	}
}

// countedHerdrLookup uses a fake login shell whose invocations and selected PATH
// are files, so the cache tests exercise the real login-shell lookup.
func countedHerdrLookup(t *testing.T) (*Daemon, string, string, string, *logBuf) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	bin := fakeHarnessScript(t, dir, "herdr")
	calls, search := filepath.Join(home, "calls"), filepath.Join(home, "search")
	if err := os.WriteFile(search, []byte(dir+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(home, "zsh")
	script := "#!/bin/sh\necho lookup >> '" + calls + "'\nread -r search < '" + search + "'\nprintf '__AGENT_NET_PATH__%s__AGENT_NET_PATH__' \"$search\"\n"
	if err := os.WriteFile(shell, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	logs := &logBuf{}
	d := &Daemon{cfg: Config{HarnessSearch: &harnesses.Options{Home: home, Dirs: []string{}}}, log: log.New(logs, "", 0)}
	return d, bin, calls, search, logs
}

func assertHerdrShellLookups(t *testing.T, calls string, want int) {
	t.Helper()
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "lookup\n"); got != want {
		t.Fatalf("login shell ran %d times, want %d", got, want)
	}
}

func TestHerdrLookupCachesUntilBinaryRemoved(t *testing.T) {
	d, bin, calls, search, logs := countedHerdrLookup(t)
	for range 2 {
		if got := d.herdrBinary(""); got != bin {
			t.Fatalf("binary = %q, want %q", got, bin)
		}
	}
	assertHerdrShellLookups(t, calls, 1)
	if got := len(logs.lines("herdr binary:")); got != 1 {
		t.Fatalf("choice logged %d times, want 1", got)
	}
	replacement := fakeHarnessScript(t, t.TempDir(), "herdr")
	if err := os.WriteFile(search, []byte(filepath.Dir(replacement)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	if got := d.herdrBinary(""); got != replacement {
		t.Fatalf("binary = %q, want replacement %q", got, replacement)
	}
	assertHerdrShellLookups(t, calls, 2)
	if got := len(logs.lines("herdr binary:")); got != 2 {
		t.Fatalf("choice logged %d times, want 2", got)
	}
}

func TestHerdrLookupRefreshesAfterRunFailureWithoutLoggingSameChoice(t *testing.T) {
	d, bin, calls, _, logs := countedHerdrLookup(t)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	plan := d.openInHerdr("", "test", protocol.EnterPlan{Argv: []string{"true"}})
	if !strings.Contains(plan.Note, "failed:") {
		t.Fatalf("note = %q", plan.Note)
	}
	if got := d.herdrBinary(""); got != bin {
		t.Fatalf("binary = %q, want %q", got, bin)
	}
	assertHerdrShellLookups(t, calls, 2)
	if got := len(logs.lines("herdr binary:")); got != 1 {
		t.Fatalf("unchanged choice logged %d times, want 1", got)
	}
}

func TestHerdrLookupConcurrentCallsShareCache(t *testing.T) {
	d, bin, calls, _, logs := countedHerdrLookup(t)
	var callers sync.WaitGroup
	for range 8 {
		callers.Go(func() {
			if got := d.herdrBinary(""); got != bin {
				t.Errorf("binary = %q, want %q", got, bin)
			}
		})
	}
	callers.Wait()
	assertHerdrShellLookups(t, calls, 1)
	if got := len(logs.lines("herdr binary:")); got != 1 {
		t.Fatalf("choice logged %d times, want 1", got)
	}
}
