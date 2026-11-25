package agentfiles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentnetworkers "github.com/niparis/agent-net/plugins/claude/agent-net-workers"
)

func TestClaudeWorkersMarketplaceContent(t *testing.T) {
	fakeHome(t)
	b, err := os.ReadFile(filepath.Join(ClaudeWorkersDir(), ".claude-plugin", "marketplace.json"))
	if err == nil {
		t.Fatalf("a marketplace exists before setup wrote one: %s", b)
	}
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(ClaudeWorkersDir(), ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("marketplace.json: %v", err)
	}
	if m.Name != "agent-net" || len(m.Plugins) != 1 ||
		m.Plugins[0].Name != "agent-net-workers" || m.Plugins[0].Source != "./agent-net-workers" {
		t.Errorf("marketplace.json: %+v", m)
	}
}

func TestWriteClaudeWorkersWritesTheEmbeddedFiles(t *testing.T) {
	fakeHome(t)
	changed, err := WriteClaudeWorkers()
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("the first write says unchanged")
	}
	for name, want := range ClaudeWorkersFiles() {
		got, err := os.ReadFile(filepath.Join(ClaudeWorkersDir(), filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if name != "agent-net-workers/hooks/register.tsx" && string(got) != string(want) {
			t.Errorf("%s is not the embedded file", name)
		}
	}
	register, err := os.ReadFile(filepath.Join(ClaudeWorkersPluginDir(), "hooks", "register.tsx"))
	exe, _ := os.Executable()
	encoded, _ := json.Marshal(exe)
	if err != nil || !strings.Contains(string(register), strings.ReplaceAll(string(encoded), `"`, `\"`)) {
		t.Errorf("register does not contain the executable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ClaudeWorkersPluginDir(), "hooks", "register.test.tsx")); err == nil {
		t.Error("the test file was written")
	}
}

func TestWriteClaudeWorkersVersionTracksPluginFiles(t *testing.T) {
	fakeHome(t)
	originalRegister := append([]byte(nil), agentnetworkers.Register...)
	t.Cleanup(func() { agentnetworkers.Register = originalRegister })

	writeWithRegister := func(register string) string {
		t.Helper()
		agentnetworkers.Register = []byte(register)
		if _, err := WriteClaudeWorkers(); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(ClaudeWorkersPluginDir(), ".claude-plugin", "plugin.json"))
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(b, &manifest); err != nil {
			t.Fatal(err)
		}
		return manifest.Version
	}

	first := writeWithRegister("first register content")
	second := writeWithRegister("second register content")
	firstAgain := writeWithRegister("first register content")
	if first == "0.1.0" || second == "0.1.0" {
		t.Fatalf("written plugin version did not include a content hash: first=%q second=%q", first, second)
	}
	if first == second {
		t.Fatalf("different plugin contents have the same version %q", first)
	}
	if first != firstAgain {
		t.Fatalf("same plugin contents have different versions %q and %q", first, firstAgain)
	}
}

// An upgrade rewrites the folder, so a session reloads the mod of this binary.
func TestSyncClaudeWorkersRefreshesAnOldCopy(t *testing.T) {
	fakeHome(t)
	if rs := Sync(); byName(rs)["claude workers mod"].Changed {
		t.Fatal("sync wrote a marketplace before setup made one")
	}
	if _, err := os.Stat(ClaudeWorkersDir()); err == nil {
		t.Fatal("sync created the marketplace folder")
	}
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	wantRegister := read(t, filepath.Join(ClaudeWorkersPluginDir(), "hooks", "register.tsx"))
	write(t, filepath.Join(ClaudeWorkersPluginDir(), "hooks", "register.tsx"), "old mod")
	write(t, filepath.Join(ClaudeWorkersDir(), ".claude-plugin", "marketplace.json"), "{}\n")
	r := SyncClaudeWorkers()
	if !r.Changed || r.Text != "updated" || r.Err != nil {
		t.Fatalf("%+v", r)
	}
	if got := read(t, filepath.Join(ClaudeWorkersPluginDir(), "hooks", "register.tsx")); got != wantRegister {
		t.Error("register.tsx was not refreshed")
	}
	if got := read(t, filepath.Join(ClaudeWorkersDir(), ".claude-plugin", "marketplace.json")); !strings.Contains(got, `"agent-net-workers"`) {
		t.Errorf("marketplace.json was not refreshed: %s", got)
	}
	if r := SyncClaudeWorkers(); r.Changed || r.Text != "ok (unchanged)" {
		t.Fatalf("second sync: %+v", r)
	}
}

// The daemon puts the link back only when setup recorded the owner's yes. A
// machine that never asked gets no link, and the folder alone is not enough.
func TestSyncClaudeWorkersRestoresTheLinkOnlyAfterAYes(t *testing.T) {
	h := fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	link := ClaudeWorkersSkillsLink()
	if r := SyncClaudeWorkers(); r.Changed || r.Text != "skipped (not installed)" {
		t.Fatalf("no answer: %+v", r)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("sync made a link without the owner's yes")
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true,"dir":"`+ClaudeWorkersDir()+`"}`)
	if r := SyncClaudeWorkers(); !r.Changed || r.Err != nil {
		t.Fatalf("after a yes: %+v", r)
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("no link at %s: %v", link, err)
	}
	if target != ClaudeWorkersPluginDir() {
		t.Errorf("link points at %q, want %q", target, ClaudeWorkersPluginDir())
	}
	if _, err := os.Stat(filepath.Join(h, ".claude", "settings.json")); err == nil {
		t.Error("the sync wrote a Claude settings file")
	}
}

// A foreign entry with the link's name is never replaced. The sync says so,
// with the one command that fixes it.
func TestSyncClaudeWorkersLeavesAForeignEntryAndReportsIt(t *testing.T) {
	fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	foreign := ClaudeWorkersSkillsLink()
	write(t, filepath.Join(foreign, "theirs.txt"), "keep me")

	r := SyncClaudeWorkers()
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if !strings.Contains(r.Note, "not agent-net's link") || r.Text != "off (a foreign entry has the name)" {
		t.Errorf("result: %+v", r)
	}
	if got := read(t, filepath.Join(foreign, "theirs.txt")); got != "keep me" {
		t.Error("the foreign entry was changed")
	}
}

// A user's own disable in Claude Code is reported, not undone.
func TestSyncClaudeWorkersReportsAUserDisable(t *testing.T) {
	fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	if err := os.MkdirAll(filepath.Dir(ClaudeWorkersSkillsLink()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ClaudeWorkersPluginDir(), ClaudeWorkersSkillsLink()); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home(), ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersSkillsID+`":false}}`)

	r := SyncClaudeWorkers()
	if r.Text != "off (disabled in Claude Code)" || !strings.Contains(r.Note, "claude plugin enable "+ClaudeWorkersSkillsID) {
		t.Errorf("result: %+v", r)
	}
	if got := read(t, settings); !strings.Contains(got, `"`+ClaudeWorkersSkillsID+`":false`) {
		t.Error("the user's disable was changed")
	}
	if _, err := os.Lstat(ClaudeWorkersSkillsLink()); err != nil {
		t.Error("the link was removed")
	}
}

// An old marketplace copy that is enabled and installed is the window. The
// sync takes agent-net's own link out so a lost marketplace registration
// cannot load both copies. When the old entry is lost, the link comes back.
func TestSyncClaudeWorkersKeepsOneWindow(t *testing.T) {
	fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	write(t, ClaudeWorkersStatePath(), `{"asked":true,"installed":true}`)
	link := ClaudeWorkersSkillsLink()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ClaudeWorkersPluginDir(), link); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home(), ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true}}`)
	write(t, filepath.Join(home(), ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"`+ClaudeWorkersOldID+`":[{"scope":"user"}]}}`)

	r := SyncClaudeWorkers()
	if r.Text != "on (old marketplace copy)" {
		t.Errorf("old copy runs: %+v", r)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Error("both copies can load: the link was left in the skills folder")
	}
	write(t, settings, `{}`)
	r = SyncClaudeWorkers()
	if !r.Changed || r.Err != nil {
		t.Fatalf("old entry lost: %+v", r)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("the link was not put back after the old entry was lost")
	}
	if s := ClaudeWorkersStatusNow(); s.OldRuns() || !s.Live() {
		t.Errorf("status after the move: %+v", s)
	}
}

// The status reads enabledPlugins without touching the settings file.
func TestClaudeWorkersStatusReadsTheLiveState(t *testing.T) {
	fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(home(), ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true}}`)
	write(t, filepath.Join(home(), ".claude", "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"`+ClaudeWorkersOldID+`":[{"scope":"user"}]}}`)
	s := ClaudeWorkersStatusNow()
	if s.LinkOK || !s.OldEnabled || !s.OldInstalled || !s.OldRuns() || !s.Live() {
		t.Errorf("old copy: %+v", s)
	}
	if err := os.MkdirAll(filepath.Dir(ClaudeWorkersSkillsLink()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ClaudeWorkersPluginDir(), ClaudeWorkersSkillsLink()); err != nil {
		t.Fatal(err)
	}
	s = ClaudeWorkersStatusNow()
	if !s.LinkOK || !s.OldEnabled || !s.OldRuns() || s.Disabled || !s.Live() {
		t.Errorf("old copy plus link: %+v", s)
	}
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersSkillsID+`":false}}`)
	s = ClaudeWorkersStatusNow()
	if !s.LinkOK || !s.Disabled || s.Live() {
		t.Errorf("disabled: %+v", s)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Fatal(err)
	}
}

// A settings file that is a link is never written: the old marketplace
// entries stay and the skills link is ready for the day they are lost.
func TestMigrateClaudeWorkersLeavesASettingsLinkAlone(t *testing.T) {
	h := fakeHome(t)
	bin := filepath.Join(h, "bin")
	write(t, filepath.Join(bin, "claude"), "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	dotfiles := filepath.Join(h, "dotfiles", "settings.json")
	write(t, dotfiles, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
	settings := filepath.Join(h, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfiles, settings); err != nil {
		t.Fatal(err)
	}
	note := MigrateClaudeWorkers()
	if !strings.Contains(note, "not agent-net's to write") {
		t.Errorf("note: %q", note)
	}
	if got := read(t, dotfiles); !strings.Contains(got, ClaudeWorkersOldID) {
		t.Error("the linked settings file was changed")
	}
	if s := ClaudeWorkersStatusNow(); !s.SettingsLinked || !s.OldEnabled || s.SettingsWritable {
		t.Errorf("status: %+v", s)
	}
}

// When the settings file is agent-net's to write, the old marketplace plugin
// goes after Claude Code lists the skills copy.
func TestMigrateClaudeWorkersRemovesTheOldCopy(t *testing.T) {
	h := fakeHome(t)
	if _, err := WriteClaudeWorkers(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureClaudeWorkersLink(); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(h, ".claude", "settings.json")
	write(t, settings, `{"enabledPlugins":{"`+ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`)
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
`)
	if err := os.Chmod(filepath.Join(bin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if note := MigrateClaudeWorkers(); note != "" {
		t.Errorf("note: %q", note)
	}
	got := read(t, calls)
	for _, want := range []string{
		"plugin list --json",
		"plugin uninstall " + ClaudeWorkersOldID + " -s user",
		"plugin marketplace remove agent-net",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in the claude calls:\n%s", want, got)
		}
	}
	if s := ClaudeWorkersStatusNow(); s.OldEnabled || s.OldRegistered {
		t.Errorf("the old entries stayed: %+v", s)
	}
}
