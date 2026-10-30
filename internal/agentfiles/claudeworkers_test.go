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
		if string(got) != string(want) {
			t.Errorf("%s is not the embedded file", name)
		}
	}
	want := filepath.Join(ClaudeWorkersPluginDir(), "hooks", "register.tsx")
	if got, _ := os.ReadFile(want); string(got) != string(agentnetworkers.Register) {
		t.Error("hooks/register.tsx is not the embedded one")
	}
	if _, err := os.Stat(filepath.Join(ClaudeWorkersPluginDir(), "hooks", "register.test.tsx")); err == nil {
		t.Error("the test file was written")
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
	write(t, filepath.Join(ClaudeWorkersPluginDir(), "hooks", "register.tsx"), "old mod")
	write(t, filepath.Join(ClaudeWorkersDir(), ".claude-plugin", "marketplace.json"), "{}\n")
	r := SyncClaudeWorkers()
	if !r.Changed || r.Text != "updated" || r.Err != nil {
		t.Fatalf("%+v", r)
	}
	if got := read(t, filepath.Join(ClaudeWorkersPluginDir(), "hooks", "register.tsx")); got != string(agentnetworkers.Register) {
		t.Error("register.tsx was not refreshed")
	}
	if got := read(t, filepath.Join(ClaudeWorkersDir(), ".claude-plugin", "marketplace.json")); !strings.Contains(got, `"agent-net-workers"`) {
		t.Errorf("marketplace.json was not refreshed: %s", got)
	}
	if r := SyncClaudeWorkers(); r.Changed || r.Text != "ok (unchanged)" {
		t.Fatalf("second sync: %+v", r)
	}
}
