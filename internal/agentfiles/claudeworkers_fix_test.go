package agentfiles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentnetworkers "github.com/niparis/agent-net/plugins/claude/agent-net-workers"
)

// fakeClaude puts a claude script in the temp home and puts it first on PATH.
func fakeClaude(t *testing.T, h, script string) string {
	t.Helper()
	bin := filepath.Join(h, "bin")
	mkdirs(t, bin)
	path := filepath.Join(bin, "claude")
	write(t, path, script)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func oldInstalled(t *testing.T, h, version string) {
	t.Helper()
	write(t, filepath.Join(h, ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"`+ClaudeWorkersOldID+`":[{"scope":"user","version":"`+version+`"}]}}`)
}

// A link to somewhere else is foreign too. agent-net must not replace it.
func TestClaudeWorkersStatusCallsALinkElsewhereForeign(t *testing.T) {
	fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(home(), "elsewhere")
	mkdirs(t, elsewhere)
	link := ClaudeWorkersSkillsLink()
	mkdirs(t, filepath.Dir(link))
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	s := ClaudeWorkersStatusNow()
	if s.LinkOK || s.Foreign != link {
		t.Errorf("status: %+v", s)
	}
	if _, foreign, err := EnsureClaudeWorkersLink(); err != nil || foreign != link {
		t.Errorf("ensure: foreign=%q err=%v", foreign, err)
	}
	if target, err := os.Readlink(link); err != nil || target != elsewhere {
		t.Errorf("the foreign link was changed: %q %v", target, err)
	}
}

// An explicit false for either id is respected; a missing entry is not a
// disable, because that is the state the owner's dotfiles lose it in.
func TestClaudeWorkersStatusRespectsAnExplicitFalse(t *testing.T) {
	fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home(), ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":false}}`)
	oldInstalled(t, home(), ClaudeWorkersPanelVersion())
	if s := ClaudeWorkersStatusNow(); !s.Disabled || s.Live() {
		t.Errorf("old id false: %+v", s)
	}
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersSkillsID+`":false}}`)
	if s := ClaudeWorkersStatusNow(); !s.Disabled || s.Live() {
		t.Errorf("skills id false: %+v", s)
	}
	write(t, settings, `{}`)
	if s := ClaudeWorkersStatusNow(); s.Disabled || !s.Live() {
		t.Errorf("a missing entry was read as a disable: %+v", s)
	}
}

// The start path writes files and the link only. A claude command on it would
// delay the daemon when claude is slow or broken.
func TestSyncClaudeWorkersRunsNoClaudeCommand(t *testing.T) {
	h := fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	write(t, filepath.Join(h, ".claude", "settings.json"), `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true}}`)
	oldInstalled(t, h, "0.1.0+old")
	calls := filepath.Join(h, "calls")
	fakeClaude(t, h, "#!/bin/sh\necho \"$*\" >> \""+calls+"\"\n")
	if r := SyncClaudeWorkers(); r.Err != nil {
		t.Fatal(r.Err)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Errorf("the sync ran a claude command:\n%s", read(t, calls))
	}
}

// A missing or blocked window is logged once per state change, not at every
// sync.
func TestSyncClaudeWorkersLogsAProblemOncePerState(t *testing.T) {
	h := fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	write(t, filepath.Join(h, ".claude", "settings.json"), `{"extraKnownMarketplaces":{"agent-net":{}}}`)
	oldInstalled(t, h, ClaudeWorkersPanelVersion())

	first := SyncClaudeWorkers()
	if !strings.Contains(first.Note, "claude plugin uninstall "+ClaudeWorkersOldID) {
		t.Fatalf("first sync: %+v", first)
	}
	if second := SyncClaudeWorkers(); second.Note != "" {
		t.Errorf("the same state was logged again: %+v", second)
	}
	link := ClaudeWorkersSkillsLink()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	mkdirs(t, link)
	foreign := SyncClaudeWorkers()
	if !strings.Contains(foreign.Note, "not agent-net's link") {
		t.Errorf("a new state logged no note: %+v", foreign)
	}
	if again := SyncClaudeWorkers(); again.Note != "" {
		t.Errorf("the foreign state was logged again: %+v", again)
	}
}

// A changed panel reaches the old marketplace copy: the refresh commands tell
// Claude Code to install the new version, and they do not write settings.json.
func TestRefreshClaudeWorkersDeliversAChangedPanel(t *testing.T) {
	h := fakeHome(t)
	originalRegister := append([]byte(nil), agentnetworkers.Register...)
	t.Cleanup(func() { agentnetworkers.Register = originalRegister })
	agentnetworkers.Register = []byte("old panel")
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(h, ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
	oldInstalled(t, h, "0.1.0+old")
	settingsBefore := read(t, settings)

	cache := filepath.Join(h, "cache")
	agentnetworkers.Register = []byte("new panel marker")
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	fakeClaude(t, h, `#!/bin/sh
case "$1 $2" in
  "plugin marketplace") exit 0 ;;
  "plugin update") mkdir -p "`+cache+`" && cp -R "`+ClaudeWorkersPluginDir()+`"/. "`+cache+`/" ;;
esac
`)
	if failures := RefreshClaudeWorkers(context.Background()); len(failures) != 0 {
		t.Fatalf("refresh: %v", failures)
	}
	if got := read(t, filepath.Join(cache, "hooks", "register.tsx")); !strings.Contains(got, "new panel marker") {
		t.Error("the old copy did not get the changed panel")
	}
	if after := read(t, settings); after != settingsBefore {
		t.Errorf("the refresh wrote settings.json:\nbefore %s\nafter  %s", settingsBefore, after)
	}
}

func TestClaudeWorkersOldNeedsRefresh(t *testing.T) {
	h := fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(h, ".claude", "settings.json"), `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
	oldInstalled(t, h, ClaudeWorkersPanelVersion())
	if s := ClaudeWorkersStatusNow(); s.OldNeedsRefresh() {
		t.Errorf("the installed panel is current: %+v", s)
	}
	oldInstalled(t, h, "0.1.0+old")
	if s := ClaudeWorkersStatusNow(); !s.OldNeedsRefresh() {
		t.Errorf("a changed panel is not refreshed: %+v", s)
	}
	write(t, filepath.Join(h, ".claude", "settings.json"), `{}`)
	if s := ClaudeWorkersStatusNow(); s.OldNeedsRefresh() {
		t.Errorf("a copy that does not run is refreshed: %+v", s)
	}
}

// ClaudeWorkersProblem names one step that works for every off state, and is
// empty when the window is on or the owner turned it off.
func TestClaudeWorkersProblemNamesAStepForEachState(t *testing.T) {
	cases := []struct {
		name string
		s    ClaudeWorkersStatus
		want string
	}{
		{"live", ClaudeWorkersStatus{LinkOK: true}, ""},
		{"old runs", ClaudeWorkersStatus{LinkOK: true, OldEnabled: true, OldInstalled: true}, ""},
		{"user disabled", ClaudeWorkersStatus{LinkOK: true, SkillsOff: true, Disabled: true}, ""},
		{"old disabled by the user", ClaudeWorkersStatus{LinkOK: true, OldOff: true, OldInstalled: true, Marketplace: true, OldMarketplace: true, Disabled: true}, ""},
		{"false written after the loss", ClaudeWorkersStatus{LinkOK: true, OldOff: true, OldInstalled: true, KnownMarketplace: true, OldMarketplace: true, Disabled: true}, "claude plugin uninstall " + ClaudeWorkersOldID},
		{"foreign", ClaudeWorkersStatus{Foreign: "/x/skills/agent-net-workers"}, "not agent-net's link"},
		{"blocked", ClaudeWorkersStatus{LinkOK: true, OldInstalled: true, OldMarketplace: true}, "claude plugin uninstall " + ClaudeWorkersOldID},
		{"link missing", ClaudeWorkersStatus{}, "agent-net setup --claude-workers-mod"},
	}
	for _, c := range cases {
		got := ClaudeWorkersProblem(c.s)
		if c.want == "" && got != "" {
			t.Errorf("%s: %q", c.name, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
