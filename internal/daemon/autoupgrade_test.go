package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/upgrade"
)

// served makes a Config option: the gateway "serves" whatever v points at.
func served(v *atomic.Value) func(*Config) {
	return func(c *Config) {
		c.AutoUpgradeMaxDelay = -1
		c.Served = func(context.Context) (string, error) { return v.Load().(string), nil }
	}
}

func servedValue(v string) *atomic.Value {
	var a atomic.Value
	a.Store(v)
	return &a
}

// autoNode starts a node whose install is counted. A successful install makes
// the restarted daemon report the new version.
func autoNode(t *testing.T, g *testGateway, version string, install func(target string) (string, error), opts ...func(*Config)) (*upNode, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	var id atomic.Value // the node ID: the first install can run before startUpNode returns
	opts = append(opts, func(c *Config) { id.Store(c.NodeID) })
	n := startUpNode(t, g, version, func(target string) (string, error) {
		calls.Add(1)
		to, err := install(target)
		if err == nil {
			installedMu.Lock()
			installedVersions[id.Load().(string)] = to
			installedMu.Unlock()
		}
		return to, err
	}, opts...)
	return n, &calls
}

func installOK(target string) (string, error) { return target, nil }

func nodeVersion(g *testGateway, id string) string {
	v, _ := g.gw.Store().NodeVersion(id)
	return v
}

// stays waits long enough for an automatic upgrade that would happen to have
// happened.
func stays(t *testing.T) { t.Helper(); time.Sleep(300 * time.Millisecond) }

func TestAutoUpgradeNewer(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n, calls := autoNode(t, g, "v0.1.0", installOK, served(servedValue("v0.2.0")))
	eventually(t, "the gateway sees the new version", func() bool { return nodeVersion(g, n.cfg.NodeID) == "v0.2.0" })
	if calls.Load() != 1 {
		t.Errorf("install called %d times, want 1", calls.Load())
	}
	// Back on the served version: no loop.
	stays(t)
	if calls.Load() != 1 {
		t.Errorf("install called %d times after the restart", calls.Load())
	}
}

func TestAutoUpgradeDoesNothing(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	for _, c := range []struct {
		name, version, served string
		opt                   func(*Config)
	}{
		{"equal", "v0.2.0", "v0.2.0", nil},
		{"node is newer", "v0.3.0", "v0.2.0", nil},
		{"dev build", "dev", "v0.2.0", nil},
		{"opt-out", "v0.1.0", "v0.2.0", func(c *Config) { c.NoAutoUpgrade = true }},
		{"served is not a release", "v0.1.0", "v0.2.0-3-gabc-dirty", nil},
	} {
		opts := []func(*Config){served(servedValue(c.served))}
		if c.opt != nil {
			opts = append(opts, c.opt)
		}
		n, calls := autoNode(t, g, c.version, installOK, opts...)
		stays(t)
		if calls.Load() != 0 {
			t.Errorf("%s: install was called", c.name)
		}
		if v := nodeVersion(g, n.cfg.NodeID); v != c.version {
			t.Errorf("%s: gateway has version %q, want %q", c.name, v, c.version)
		}
	}
}

func TestAutoUpgradeFailureIsRemembered(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	sv := servedValue("v0.2.0")
	n, calls := autoNode(t, g, "v0.1.0", func(string) (string, error) {
		return "", errors.New("checksum mismatch for agent-net-linux-amd64")
	}, served(sv))
	eventually(t, "one failed attempt", func() bool { return calls.Load() == 1 })
	eventually(t, "failure recorded", func() bool {
		f := upgrade.LoadAuto(n.cfg.StateDir).Failed
		return f != nil && f.Version == "v0.2.0" && f.Reason != "" && time.Since(f.At) < time.Minute
	})

	// Reconnect twice: same version, inside the hour: no new attempt.
	for i := 0; i < 2; i++ {
		n.daemon().Stop()
		n.start()
		eventually(t, "reconnected", n.daemon().Connected)
		stays(t)
	}
	if calls.Load() != 1 {
		t.Fatalf("install called %d times over reconnects, want 1", calls.Load())
	}
	if v := nodeVersion(g, n.cfg.NodeID); v != "v0.1.0" {
		t.Errorf("node moved to %q", v)
	}

	// A different version is tried at once.
	sv.Store("v0.3.0")
	n.daemon().Stop()
	n.start()
	eventually(t, "second version tried", func() bool { return calls.Load() == 2 })

	// After an hour the first version would be tried again.
	st := upgrade.LoadAuto(n.cfg.StateDir)
	st.Failed.Version, st.Failed.At = "v0.3.0", time.Now().Add(-2*time.Hour)
	st.Save(n.cfg.StateDir)
	sv.Store("v0.3.0")
	n.daemon().Stop()
	n.start()
	eventually(t, "retry after the hour", func() bool { return calls.Load() == 3 })
}

func TestAutoUpgradeHold(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	sv := servedValue("v0.2.0")
	n, calls := autoNode(t, g, "v0.1.0", installOK, served(sv), func(c *Config) {
		if err := upgrade.SetHold(c.StateDir, "v0.2.0"); err != nil {
			t.Fatal(err)
		}
	})
	stays(t)
	if calls.Load() != 0 {
		t.Fatal("upgraded to a held version")
	}
	if upgrade.LoadAuto(n.cfg.StateDir).Hold != "v0.2.0" {
		t.Fatal("the hold was lost")
	}

	// A newer release lifts the hold and installs.
	sv.Store("v0.3.0")
	n.daemon().Stop()
	n.start()
	eventually(t, "upgraded past the hold", func() bool { return nodeVersion(g, n.cfg.NodeID) == "v0.3.0" })
	if h := upgrade.LoadAuto(n.cfg.StateDir).Hold; h != "" {
		t.Errorf("hold still %q", h)
	}
}

// The gateway's node.upgrade request and the automatic upgrade share one
// guard: while one install runs, the other does not start a second one.
func TestAutoUpgradeSharesTheInstallGuard(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	n, calls := autoNode(t, g, "v0.1.0", func(target string) (string, error) {
		started <- struct{}{}
		<-release
		return "", errors.New("slow install failed")
	}, served(servedValue("v0.2.0")))
	<-started
	if _, _, err := n.daemon().upgradeTo(context.Background(), "v0.9.0"); !errors.Is(err, errUpgradeBusy) {
		t.Fatalf("install of another version: %v", err)
	}
	close(release)
	eventually(t, "failure recorded", func() bool { return upgrade.LoadAuto(n.cfg.StateDir).Failed != nil })
	if calls.Load() != 1 {
		t.Errorf("install called %d times", calls.Load())
	}
}
