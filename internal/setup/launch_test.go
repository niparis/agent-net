package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/daemon"
)

// Machines accept workers by default. --no-launch is saved in node.json and
// stays through later setups, a rejoin and --launch undoes it.
func TestSetupNoLaunch(t *testing.T) {
	f := newFixture(t)
	fakeClaude(t, f.home)

	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "workers accepted") {
		t.Errorf("default:\n%s", out)
	}
	nc, _, _ := daemon.LoadNode(daemon.ConfigDir())
	if nc.NoLaunch {
		t.Fatal("a fresh machine refuses workers")
	}

	out = run(t, Options{NoLaunch: true})
	if !strings.Contains(out, "workers refused (--no-launch)") {
		t.Errorf("--no-launch:\n%s", out)
	}
	if nc, _, _ = daemon.LoadNode(daemon.ConfigDir()); !nc.NoLaunch {
		t.Fatal("no_launch is not in node.json")
	}
	raw, _ := os.ReadFile(filepath.Join(f.home, "cfg", "node.json"))
	if !strings.Contains(string(raw), `"no_launch": true`) {
		t.Errorf("node.json:\n%s", raw)
	}

	// A later setup without a flag keeps the setting, and so does a rejoin.
	if out = run(t, Options{}); !strings.Contains(out, "workers refused") {
		t.Errorf("rerun:\n%s", out)
	}
	run(t, Options{Join: f.newJoin(t)})
	if nc, _, _ = daemon.LoadNode(daemon.ConfigDir()); !nc.NoLaunch {
		t.Error("a rejoin forgot --no-launch")
	}

	if out = run(t, Options{Launch: true}); !strings.Contains(out, "workers accepted") {
		t.Errorf("--launch:\n%s", out)
	}
	if nc, _, _ = daemon.LoadNode(daemon.ConfigDir()); nc.NoLaunch {
		t.Error("--launch did not undo it")
	}

	// Both flags at once is an error.
	var buf strings.Builder
	err := Setup(context.Background(), Options{NoLaunch: true, Launch: true, Out: &buf, Exe: "/opt/test/agent-net"})
	if err == nil || !strings.Contains(buf.String(), "not both") {
		t.Errorf("both flags: %v\n%s", err, buf.String())
	}
}
