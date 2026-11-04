package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/daemon"
)

func TestSetupAutoUpgradeSetting(t *testing.T) {
	f := newFixture(t)
	run(t, Options{Join: f.newJoin(t)})
	if nc, _, _ := daemon.LoadNode(daemon.ConfigDir()); nc.NoAutoUpgrade {
		t.Fatal("automatic upgrade disabled by default")
	}
	configPath := filepath.Join(daemon.ConfigDir(), "node.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "{", `{ "future_setting": "kept",`, 1))
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, Options{NoAutoUpgrade: true})
	raw, err = os.ReadFile(configPath)
	if err != nil || !strings.Contains(string(raw), `"future_setting": "kept"`) {
		t.Fatal("opt-out erased an unknown node setting")
	}
	if nc, _, _ := daemon.LoadNode(daemon.ConfigDir()); !nc.NoAutoUpgrade {
		t.Fatal("opt-out not saved")
	}
	run(t, Options{})
	run(t, Options{Join: f.newJoin(t)})
	if nc, _, _ := daemon.LoadNode(daemon.ConfigDir()); !nc.NoAutoUpgrade {
		t.Fatal("setup or rejoin lost opt-out")
	}
	run(t, Options{AutoUpgrade: true})
	if nc, _, _ := daemon.LoadNode(daemon.ConfigDir()); nc.NoAutoUpgrade {
		t.Fatal("automatic upgrades not re-enabled")
	}
	var out strings.Builder
	if err := Setup(context.Background(), Options{NoAutoUpgrade: true, AutoUpgrade: true, Out: &out, Exe: "/opt/test/agent-net"}); err == nil {
		t.Fatal("accepted both options")
	}
}
