package agentfiles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentnetworkers "github.com/niparis/agent-net/plugins/claude/agent-net-workers"
)

// When the settings file is agent-net's to write, the daemon moves an old
// enabled marketplace copy to the skills link.
func TestSyncClaudeWorkersMovesAnOldCopyWhenItMayWrite(t *testing.T) {
	h := fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	settings := filepath.Join(h, ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
	write(t, filepath.Join(h, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"`+ClaudeWorkersOldID+`":[{"scope":"user","version":"`+ClaudeWorkersPanelVersion()+`"}]}}`)
	calls := filepath.Join(h, "calls")
	bin := filepath.Join(h, "bin")
	write(t, filepath.Join(bin, "claude"), `#!/bin/sh
echo "$*" >> "`+calls+`"
case "$1 $2" in
  "plugin list") printf '%s\n' '[{"id":"`+ClaudeWorkersSkillsID+`","enabled":false}]' ;;
  "plugin marketplace") python3 - "$3" "`+settings+`" <<'PY'
import json,sys
d=json.load(open(sys.argv[2]))
d.pop("enabledPlugins",None); d.pop("extraKnownMarketplaces",None)
json.dump(d,open(sys.argv[2],"w"))
PY
  ;;
esac
exit 0
`)
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := SyncClaudeWorkers()
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if r.Text != "on (old marketplace copy)" {
		t.Errorf("result: %+v", r)
	}
	if note := MigrateClaudeWorkers(context.Background()); note != "" {
		t.Errorf("migration: %q", note)
	}
	if _, err := os.Lstat(ClaudeWorkersSkillsLink()); err != nil {
		t.Errorf("the skills link is missing: %v", err)
	}
	if s := ClaudeWorkersStatusNow(); s.OldEnabled || !s.Live() {
		t.Errorf("the old copy is still there: %+v", s)
	}
	got := read(t, calls)
	if !strings.Contains(got, "plugin marketplace remove agent-net") {
		t.Errorf("the old marketplace was not removed:\n%s", got)
	}
}

// A changed panel reaches the old copy before the migration moves it away.
func TestMigrateClaudeWorkersRefreshesAChangedPanel(t *testing.T) {
	h := fakeHome(t)
	originalRegister := append([]byte(nil), agentnetworkers.Register...)
	t.Cleanup(func() { agentnetworkers.Register = originalRegister })
	agentnetworkers.Register = []byte("old panel")
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(h, ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
	write(t, filepath.Join(h, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"`+ClaudeWorkersOldID+`":[{"scope":"user","version":"0.1.0+old"}]}}`)
	cache := filepath.Join(h, "cache")
	agentnetworkers.Register = []byte("new panel marker")
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	fakeClaude(t, h, `#!/bin/sh
case "$1 $2" in
  "plugin list") printf '%s\n' '[{"id":"`+ClaudeWorkersSkillsID+`","enabled":false}]' ;;
  "plugin update") mkdir -p "`+cache+`" && cp -R "`+ClaudeWorkersPluginDir()+`"/. "`+cache+`/" ;;
  "plugin marketplace") [ "$3" = remove ] && python3 - "$4" "`+settings+`" <<'PY'
import json,sys
d=json.load(open(sys.argv[2]))
d.pop("enabledPlugins",None); d.pop("extraKnownMarketplaces",None)
json.dump(d,open(sys.argv[2],"w"))
PY
  ;;
esac
exit 0
`)
	if note := MigrateClaudeWorkers(context.Background()); note != "" {
		t.Errorf("note: %q", note)
	}
	got, err := os.ReadFile(filepath.Join(cache, "hooks", "register.tsx"))
	if err != nil || !strings.Contains(string(got), "new panel marker") {
		t.Errorf("the old copy did not get the changed panel: %v", err)
	}
}

// countCalls is how many claude commands a fake claude recorded.
func countCalls(t *testing.T, path string) int {
	t.Helper()
	got := strings.TrimSpace(read(t, path))
	if got == "" {
		return 0
	}
	return len(strings.Split(got, "\n"))
}

// A refresh that succeeds without changing the panel counts as a failed run:
// the daemon tries the claude commands again only with a new binary version.
func TestMigrateClaudeWorkersOnceCountsAStaleRefreshOncePerVersion(t *testing.T) {
	h := fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	// A linked settings file is the owner's Mac: agent-net never writes it, so
	// the old copy stays the one that runs and only the refresh can update it.
	linked := filepath.Join(h, "dotfiles-settings.json")
	write(t, linked, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
	mkdirs(t, filepath.Join(h, ".claude"))
	if err := os.Symlink(linked, filepath.Join(h, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	oldInstalled(t, h, "0.1.0+old")
	calls := filepath.Join(h, "calls")
	fakeClaude(t, h, "#!/bin/sh\necho \"$*\" >> \""+calls+"\"\nexit 0\n")

	if note := MigrateClaudeWorkersOnce(context.Background(), "v1"); note == "" {
		t.Fatal("a refresh that left the old panel was not reported")
	}
	first := countCalls(t, calls)
	for i := 0; i < 3; i++ {
		if note := MigrateClaudeWorkersOnce(context.Background(), "v1"); note != "" {
			t.Errorf("the same version tried again: %q", note)
		}
	}
	if got := countCalls(t, calls); got != first {
		t.Errorf("claude ran again for the same version: %d then %d", first, got)
	}
	if note := MigrateClaudeWorkersOnce(context.Background(), "v2"); note == "" {
		t.Error("a new version did not try again")
	}
	if got := countCalls(t, calls); got <= first {
		t.Errorf("claude did not run for the new version: %d then %d", first, got)
	}
}

// The daemon runs no claude command unless setup recorded the owner's yes.
func TestMigrateClaudeWorkersOnceRunsNoClaudeWithoutTheOwnersYes(t *testing.T) {
	cases := []struct{ name, state string }{
		{"never asked", ""},
		{"turned off", `{"asked":true,"installed":false}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := fakeHome(t)
			if _, err := WriteClaudeWorkers(); err != nil {
				t.Fatal(err)
			}
			if _, _, err := EnsureClaudeWorkersLink(); err != nil {
				t.Fatal(err)
			}
			if c.state != "" {
				write(t, ClaudeWorkersStatePath(), c.state)
			}
			write(t, filepath.Join(h, ".claude", "settings.json"),
				`{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
			oldInstalled(t, h, "0.1.0+old")
			calls := filepath.Join(h, "calls")
			fakeClaude(t, h, "#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n")
			if note := MigrateClaudeWorkersOnce(context.Background(), "v1"); note != "" {
				t.Errorf("note: %q", note)
			}
			if _, err := os.Stat(calls); err == nil {
				t.Errorf("the migration ran claude:\n%s", read(t, calls))
			}
		})
	}
}

// The daemon refreshes a running old copy but never moves or removes it; that
// stays with agent-net setup --claude-workers-mod or the off switch.
func TestMigrateClaudeWorkersOnceNeverMovesTheOldCopy(t *testing.T) {
	h := fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	settings := filepath.Join(h, ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
	before := read(t, settings)
	installed := filepath.Join(h, ".claude", "plugins", "installed_plugins.json")
	oldInstalled(t, h, "0.1.0+old")
	calls := filepath.Join(h, "calls")
	fakeClaude(t, h, `#!/bin/sh
echo "$*" >> "`+calls+`"
case "$1 $2" in
  "plugin update")
    python3 - "`+installed+`" "`+ClaudeWorkersPanelVersion()+`" <<'PY'
import json,sys
p=sys.argv[1]
d=json.load(open(p))
d["plugins"]["`+ClaudeWorkersOldID+`"]=[{"scope":"user","version":sys.argv[2]}]
json.dump(d,open(p,"w"))
PY
  ;;
  "plugin list") printf '%s\n' '[{"id":"`+ClaudeWorkersSkillsID+`","enabled":false}]' ;;
esac
exit 0
`)
	if note := MigrateClaudeWorkersOnce(context.Background(), "v1"); note != "" {
		t.Errorf("note: %q", note)
	}
	got := read(t, calls)
	if strings.Contains(got, "uninstall") || strings.Contains(got, "marketplace remove") {
		t.Errorf("the daemon moved the old copy:\n%s", got)
	}
	if after := read(t, settings); after != before {
		t.Errorf("the daemon wrote settings.json:\nbefore %s\nafter  %s", before, after)
	}
	if s := ClaudeWorkersStatusNow(); !s.OldRuns() || s.OldNeedsRefresh() {
		t.Errorf("the running old copy was not refreshed in place: %+v", s)
	}
}

// Daemon stop cancels a running claude command instead of waiting for it. The
// fake claude hangs, and the cancel must end the migration promptly.
func TestMigrateClaudeWorkersOnceStopsOnCancel(t *testing.T) {
	h := fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	linked := filepath.Join(h, "dotfiles-settings.json")
	write(t, linked, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
	mkdirs(t, filepath.Join(h, ".claude"))
	if err := os.Symlink(linked, filepath.Join(h, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	oldInstalled(t, h, "0.1.0+old")
	calls := filepath.Join(h, "calls")
	fakeClaude(t, h, "#!/bin/sh\necho \"$*\" >> \""+calls+"\"\nexec sleep 60\n")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan string, 1)
	go func() { done <- MigrateClaudeWorkersOnce(ctx, "v1") }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(calls); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake claude did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not stop the hung claude command")
	}
}
