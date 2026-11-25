package agentfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if note := MigrateClaudeWorkers(); note != "" {
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
	if note := MigrateClaudeWorkers(); note != "" {
		t.Errorf("note: %q", note)
	}
	got, err := os.ReadFile(filepath.Join(cache, "hooks", "register.tsx"))
	if err != nil || !strings.Contains(string(got), "new panel marker") {
		t.Errorf("the old copy did not get the changed panel: %v", err)
	}
}
