package agentfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		`{"version":2,"plugins":{"`+ClaudeWorkersOldID+`":[{"scope":"user"}]}}`)
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
	r := SyncClaudeWorkers()
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	if r.Text != "created" && r.Text != "updated" {
		t.Errorf("result: %+v", r)
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
