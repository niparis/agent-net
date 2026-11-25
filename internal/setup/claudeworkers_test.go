package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/protocol"
)

func claudeCalls(t *testing.T, logPath string) []string {
	t.Helper()
	b, err := os.ReadFile(logPath)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func callsContain(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

func TestClaudeWorkersModIsInstalledWithTheOneQuestion(t *testing.T) {
	f := newFixture(t)
	logPath := fakeClaude(t, f.home)
	answer(t, "y\n")
	out := runAskingAll(t, Options{Join: f.newJoin(t)})
	for _, want := range []string{"Turn all on? [Y/n/c]", "claude workers mod on (plugin agent-net-workers in the Claude skills folder)"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(agentfiles.ClaudeWorkersPluginDir(), "hooks", "register.tsx")); err != nil {
		t.Error("the plugin was not written")
	}
	if _, err := os.Stat(filepath.Join(agentfiles.ClaudeWorkersPluginDir(), "hooks", "register.test.tsx")); err == nil {
		t.Error("the test file was written")
	}
	target, err := os.Readlink(agentfiles.ClaudeWorkersSkillsLink())
	if err != nil {
		t.Fatalf("no link in the skills folder: %v", err)
	}
	if target != agentfiles.ClaudeWorkersPluginDir() {
		t.Errorf("link points at %q, want %q", target, agentfiles.ClaudeWorkersPluginDir())
	}
	st := loadWorkersModState()
	if !st.Asked || !st.Installed || st.Dir != agentfiles.ClaudeWorkersDir() {
		t.Errorf("state: %+v", st)
	}
	for _, call := range claudeCalls(t, logPath) {
		if strings.HasPrefix(call, "plugin ") {
			t.Errorf("a new install called claude for the plugin: %q", call)
		}
	}
	if b, err := os.ReadFile(filepath.Join(f.home, ".claude", "settings.json")); err == nil && strings.Contains(string(b), "agent-net-workers") {
		t.Errorf("setup wrote a plugin entry to settings.json: %s", b)
	}

	// The owner agreed once: a later run refreshes the folder, does not ask,
	// and keeps the link.
	noTTY(t)
	out = runAskingAll(t, Options{})
	if strings.Contains(out, "[Y/n/c]") || !strings.Contains(out, "claude workers mod ok (unchanged)") {
		t.Errorf("second run:\n%s", out)
	}
}

func TestClaudeWorkersModOffWithTheOneQuestion(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	answer(t, "n\n")
	out := runAskingAll(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "claude workers mod off") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Stat(agentfiles.ClaudeWorkersSkillsLink()); err == nil {
		t.Error("a link was written on a no")
	}
	answer(t, "y\n")
	out = runAskingAll(t, Options{})
	if strings.Contains(out, "[Y/n/c]") || strings.Contains(out, "claude workers mod on") {
		t.Errorf("asked or installed after a no:\n%s", out)
	}
}

func TestClaudeWorkersModWithoutATerminal(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	out := runAskingAll(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "claude workers mod off") {
		t.Errorf("lacks the off line:\n%s", out)
	}
	if _, err := os.Stat(workersModStatePath()); err == nil {
		t.Error("a no-terminal run recorded an answer")
	}
	if _, err := os.Stat(agentfiles.ClaudeWorkersSkillsLink()); err == nil {
		t.Error("the link was written without an answer")
	}

	out = runAskingAll(t, Options{WorkersMod: WorkersModYes})
	if !strings.Contains(out, "claude workers mod on (plugin agent-net-workers in the Claude skills folder)") {
		t.Errorf("flag did not install:\n%s", out)
	}
}

// An upgrade rewrites the folder, then asks running sessions to reload it.
func TestClaudeWorkersModUpgradeRefreshesAndAsksForReload(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes})
	register := filepath.Join(agentfiles.ClaudeWorkersPluginDir(), "hooks", "register.tsx")
	if err := os.WriteFile(register, []byte("old mod"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runAskingAll(t, Options{PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo})
	for _, want := range []string{"claude workers mod updated (plugin agent-net-workers in the Claude skills folder)", "hint: open Claude Code sessions: run /reload-plugins or restart them to load the new panel"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	b, err := os.ReadFile(register)
	if err != nil || string(b) == "old mod" || string(b) == "" {
		t.Errorf("register.tsx was not refreshed: %q, %v", b, err)
	}
	if _, err := os.Lstat(agentfiles.ClaudeWorkersSkillsLink()); err != nil {
		t.Errorf("the link is missing: %v", err)
	}
}

func TestClaudeWorkersModUpdatesWhenOnlyPluginVersionIsStale(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	options := Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes}
	runAskingAll(t, options)

	manifestPath := filepath.Join(agentfiles.ClaudeWorkersPluginDir(), ".claude-plugin", "plugin.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(manifest, &fields); err != nil {
		t.Fatal(err)
	}
	oldVersion, _ := json.Marshal(fields.Version)
	manifest = bytes.Replace(manifest, append([]byte(`"version": `), oldVersion...), []byte(`"version": "0.1.0"`), 1)
	if bytes.Contains(manifest, oldVersion) && fields.Version != "0.1.0" {
		t.Fatal("failed to replace the installed plugin version")
	}
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	options.Join = ""
	options.WorkersMod = ""
	out := runAskingAll(t, options)
	if !strings.Contains(out, "claude workers mod updated") {
		t.Errorf("output:\n%s", out)
	}
	b, err := os.ReadFile(manifestPath)
	if err != nil || bytes.Contains(b, []byte(`"version": "0.1.0"`)) {
		t.Errorf("the manifest was not refreshed: %s, %v", b, err)
	}
}

// A settings file that is a link is never written. The old marketplace entries
// stay and the workers step reports why it could not move them.
func TestClaudeWorkersModDoesNotWriteALinkedSettingsFile(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	dotfiles := filepath.Join(f.home, "dotfiles", "settings.json")
	if err := os.MkdirAll(filepath.Dir(dotfiles), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"enabledPlugins":{"` + agentfiles.ClaudeWorkersOldID + `":true}}`
	if err := os.WriteFile(dotfiles, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(f.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfiles, settings); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r, err := newRunner(Options{Out: &out, Exe: "/opt/test/agent-net"})
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.Integrations{WorkersMod: true}
	r.want = &want
	r.claudeWorkersMod()
	if !strings.Contains(out.String(), "the old marketplace plugin stays:") {
		t.Errorf("output:\n%s", out.String())
	}
	if got, err := os.ReadFile(dotfiles); err != nil || string(got) != old {
		t.Errorf("the linked settings file changed: %q, %v", got, err)
	}
	if _, err := os.Lstat(agentfiles.ClaudeWorkersSkillsLink()); err != nil {
		t.Errorf("the skills link is missing: %v", err)
	}
}

// A foreign entry with the plugin's name is never replaced, and setup says so.
func TestClaudeWorkersModLeavesAForeignSkillsEntry(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	foreign := agentfiles.ClaudeWorkersSkillsLink()
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "theirs.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes})
	if !strings.Contains(out, "claude workers mod off (a foreign entry has the name)") {
		t.Errorf("output:\n%s", out)
	}
	if got, err := os.ReadFile(filepath.Join(foreign, "theirs.txt")); err != nil || string(got) != "keep" {
		t.Errorf("the foreign entry was changed: %q, %v", got, err)
	}
}

// A run with no terminal and no network answer keeps the install. A disable the
// owner chose in Claude Code is not undone.
func TestClaudeWorkersModKeepsAUserDisable(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes})
	settings := filepath.Join(f.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"enabledPlugins":{"`+agentfiles.ClaudeWorkersSkillsID+`":false}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := runAskingAll(t, Options{})
	if !strings.Contains(out, "claude workers mod off (disabled in Claude Code; the skills link is in place)") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := os.Lstat(agentfiles.ClaudeWorkersSkillsLink()); err != nil {
		t.Errorf("the link was removed: %v", err)
	}
	b, err := os.ReadFile(settings)
	var doc struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err != nil || json.Unmarshal(b, &doc) != nil || doc.EnabledPlugins[agentfiles.ClaudeWorkersSkillsID] {
		t.Errorf("the user's disable was changed: %s, %v", b, err)
	}
}

func TestUninstallRemovesTheClaudeWorkersMod(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes})

	var out bytes.Buffer
	if err := Uninstall(Options{Out: &out, NoService: true}); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out.String())
	}
	if !strings.Contains(norm(out.String()), "claude workers mod ok (removed)") {
		t.Errorf("output:\n%s", out.String())
	}
	if _, err := os.Lstat(agentfiles.ClaudeWorkersSkillsLink()); err == nil {
		t.Error("the skills link is still there")
	}
	if _, err := os.Stat(agentfiles.ClaudeWorkersDir()); err == nil {
		t.Error("the plugin folder is still there")
	}
	if _, err := os.Stat(workersModStatePath()); err == nil {
		t.Error("the state file is still there")
	}
}

func TestUninstallSkipsTheClaudeWorkersModWhenSetupNeverWroteIt(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)
	var out bytes.Buffer
	if err := Uninstall(Options{Out: &out, NoService: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(norm(out.String()), "claude workers mod skipped (not installed)") {
		t.Errorf("output:\n%s", out.String())
	}
}

// A fake claude that reports the skills plugin and can drop the old entries.
func fakeClaudeWithPlugins(t *testing.T, home, settings string) string {
	t.Helper()
	bin := t.TempDir()
	calls := filepath.Join(home, "claude-plugin-calls")
	script := `#!/bin/sh
echo "$*" >> "` + calls + `"
case "$1 $2" in
  "plugin list") printf '%s\n' '[{"id":"` + agentfiles.ClaudeWorkersSkillsID + `","enabled":false}]' ;;
  "plugin marketplace")
    python3 - "$3" "` + settings + `" <<'PY'
import json,sys
d=json.load(open(sys.argv[2]))
d.pop("enabledPlugins",None); d.pop("extraKnownMarketplaces",None)
json.dump(d,open(sys.argv[2],"w"))
PY
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

// When the settings file is agent-net's to write, setup removes the old
// marketplace plugin after Claude Code lists the skills copy.
func TestSetupMovesTheOldMarketplaceCopy(t *testing.T) {
	f := newFixture(t)
	settings := filepath.Join(f.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"enabledPlugins":{"`+agentfiles.ClaudeWorkersOldID+`":true},"extraKnownMarketplaces":{"agent-net":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(f.home, ".claude", "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte(`{"version":2,"plugins":{"`+agentfiles.ClaudeWorkersOldID+`":[{"scope":"user"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := fakeClaudeWithPlugins(t, f.home, settings)
	out := runAskingAll(t, Options{Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo, PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes})
	if !strings.Contains(out, "claude workers mod on (plugin agent-net-workers in the Claude skills folder)") {
		t.Errorf("output:\n%s", out)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"plugin list --json",
		"plugin uninstall " + agentfiles.ClaudeWorkersOldID + " -s user",
		"plugin marketplace remove agent-net",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("no %q in the claude calls:\n%s", want, b)
		}
	}
	if got, err := os.ReadFile(settings); err != nil || strings.Contains(string(got), agentfiles.ClaudeWorkersOldID) {
		t.Errorf("the old entry stayed: %s, %v", got, err)
	}
}

// A claude CLI that cannot list plugins does not stop setup; the old copy
// stays and setup says why.
func TestClaudeWorkersModCLIFailureIsAStepResult(t *testing.T) {
	f := newFixture(t)
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$*\" >> '" + filepath.Join(f.home, "claude.log") + "'\n" +
		"case \"$1 $2\" in\n" +
		"  'plugin list') echo 'boom'; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	settings := filepath.Join(f.home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"enabledPlugins":{"`+agentfiles.ClaudeWorkersOldID+`":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := Setup(context.Background(), Options{
		Join: f.newJoin(t), PeerMessages: PeerMessagesNo, HintHook: HintHookNo,
		PolicyHook: PolicyHookNo, WorkersMod: WorkersModYes, Out: &out, Exe: "/opt/test/agent-net",
	})
	if err != nil {
		t.Fatalf("setup failed on a claude error: %v\n%s", err, out.String())
	}
	if got := norm(out.String()); !strings.Contains(got, "hint: the old marketplace plugin stays: Claude Code does not list") {
		t.Errorf("output:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(agentfiles.ClaudeWorkersPluginDir(), ".claude-plugin", "plugin.json")); err != nil {
		t.Error("the files were not written")
	}
	register, err := os.ReadFile(filepath.Join(agentfiles.ClaudeWorkersPluginDir(), "hooks", "register.tsx"))
	if err != nil || !strings.Contains(string(register), `\"/opt/test/agent-net\"`) {
		t.Errorf("installed binary path missing from register: %v", err)
	}
	if st := loadWorkersModState(); !st.Installed {
		t.Errorf("state does not record the owner's yes: %+v", st)
	}
}
