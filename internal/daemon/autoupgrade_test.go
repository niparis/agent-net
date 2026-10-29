package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
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

// After `agent-net upgrade` restarts the gateway, the nodes that were online
// are only reconnecting. They must be reported ok, not offline: both a node
// that waits to be asked, and one that catches up by itself on reconnect.
func TestNodesReconnectingAfterGatewayRestartAreNotOffline(t *testing.T) {
	dir := t.TempDir()
	g := startGateway(t, dir, "")
	addr := g.addr
	waits, _ := autoNode(t, g, "v0.1.0", installOK, served(servedValue("v0.1.0")))
	sv := servedValue("v0.1.0")
	catches, _ := autoNode(t, g, "v0.1.0", installOK, served(sv))
	off := startUpNode(t, g, "v0.1.0", nil)
	off.daemon().Stop()
	eventually(t, "off node is gone", func() bool {
		info, _ := g.gw.NodesInfo()
		for _, n := range info.Nodes {
			if n.NodeID == off.cfg.NodeID {
				return !n.Online
			}
		}
		return false
	})

	// The gateway moves to v0.2.0 and restarts.
	sv.Store("v0.2.0")
	g.stop()
	g2 := startGateway(t, dir, addr)
	expect := []string{waits.cfg.NodeID, catches.cfg.NodeID}
	res, err := g2.gw.UpgradeNodesExpect(context.Background(), "v0.2.0", 20*time.Second, expect)
	if err != nil {
		t.Fatal(err)
	}
	got := byNode(res)
	for _, id := range expect {
		if r := got[id]; r.Status != protocol.UpgradeOK || r.To != "v0.2.0" {
			t.Errorf("node %s: %+v", id, r)
		}
	}
	if r := got[off.cfg.NodeID]; r.Status != protocol.UpgradeOffline || r.Detail != gateway.OfflineDetail {
		t.Errorf("offline node: %+v", r)
	}
}

// The gateway's request for the version an automatic upgrade is already
// installing joins it: one install, and the gateway sees ok.
func TestPushJoinsARunningAutoUpgrade(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	n, calls := autoNode(t, g, "v0.1.0", func(target string) (string, error) {
		started <- struct{}{}
		<-release
		return target, nil
	}, served(servedValue("v0.2.0")))
	<-started
	type out struct {
		res []protocol.UpgradeResult
		err error
	}
	ch := make(chan out, 1)
	go func() {
		res, err := g.gw.UpgradeNodes(context.Background(), "v0.2.0", 10*time.Second)
		ch <- out{res, err}
	}()
	time.Sleep(200 * time.Millisecond) // the request reaches the daemon while the install runs
	close(release)
	o := <-ch
	if o.err != nil {
		t.Fatal(o.err)
	}
	var got *protocol.UpgradeResult
	for i := range o.res {
		if o.res[i].NodeID == n.cfg.NodeID {
			got = &o.res[i]
		}
	}
	if got == nil || got.Status != protocol.UpgradeOK || got.To != "v0.2.0" {
		t.Fatalf("push result: %+v", o.res)
	}
	// One install, and one restart: the node is back on v0.2.0 and stays up.
	eventually(t, "back on v0.2.0", func() bool { return nodeVersion(g, n.cfg.NodeID) == "v0.2.0" })
	time.Sleep(600 * time.Millisecond)
	if calls.Load() != 1 || !n.daemon().Connected() {
		t.Errorf("installs %d, connected %v, want 1 and true", calls.Load(), n.daemon().Connected())
	}
}
