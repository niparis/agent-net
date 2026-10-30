package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// upNode is a daemon with a version and fake install and restart steps.
type upNode struct {
	t   *testing.T
	cfg Config
	mu  sync.Mutex
	d   *Daemon
	ver string // the version after an install; empty until then
}

func (n *upNode) daemon() *Daemon {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.d
}

func (n *upNode) start() {
	n.t.Helper()
	n.mu.Lock()
	cfg := n.cfg
	if n.ver != "" {
		cfg.Version = n.ver
	}
	n.mu.Unlock()
	d, err := New(cfg)
	if err != nil {
		n.t.Fatal(err)
	}
	d.cmdline = func(int) string { return "pi" }
	// Set before Start: an automatic upgrade can restart the node at once.
	n.mu.Lock()
	n.d = d
	n.mu.Unlock()
	if err := d.Start(context.Background()); err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(d.Stop)
}

// startUpNode joins a new node. install is what "download and replace" does
// and returns the new version; nil means it fails with installErr.
func startUpNode(t *testing.T, g *testGateway, version string, install func(target string) (string, error), opts ...func(*Config)) *upNode {
	t.Helper()
	cfgDir := t.TempDir()
	if _, err := Join(context.Background(), g.joinString(), cfgDir); err != nil {
		t.Fatal(err)
	}
	nc, ca, err := LoadNode(cfgDir)
	if err != nil {
		t.Fatal(err)
	}
	n := &upNode{t: t}
	n.cfg = Config{GatewayURL: nc.GatewayURL, NodeID: nc.NodeID, Token: nc.Token, CAPEM: ca,
		Hostname: "host-" + nc.NodeID, StateDir: t.TempDir(), SocketPath: filepath.Join(shortDir(t), "d.sock"),
		ClaudeSessionsDir: t.TempDir(), BackoffMin: 20 * time.Millisecond, BackoffMax: 100 * time.Millisecond,
		Version: version}
	if install != nil {
		n.cfg.Upgrade = func(ctx context.Context, target string) (string, string, error) {
			to, err := install(target)
			return version, to, err
		}
	}
	for _, o := range opts {
		o(&n.cfg)
	}
	userRestart := n.cfg.Restart
	n.cfg.Restart = func() {
		if userRestart != nil {
			userRestart()
			return
		}
		// What a service manager does: stop this process, start the new binary.
		old := n.daemon()
		old.Stop()
		v := n.installed()
		n.mu.Lock()
		n.ver = v
		n.mu.Unlock()
		n.start()
	}
	n.start()
	eventually(t, "node connected", func() bool { return n.daemon().Connected() })
	return n
}

var installedMu sync.Mutex
var installedVersions = map[string]string{}

func (n *upNode) installed() string {
	installedMu.Lock()
	defer installedMu.Unlock()
	if v := installedVersions[n.cfg.NodeID]; v != "" {
		return v
	}
	return n.cfg.Version
}

func byNode(res []protocol.UpgradeResult) map[string]protocol.UpgradeResult {
	m := map[string]protocol.UpgradeResult{}
	for _, r := range res {
		m[r.NodeID] = r
	}
	return m
}

func TestGatewayUpgradesNodes(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")

	// ok: installs v2, restarts, comes back reporting v2.
	var ok *upNode
	ok = startUpNode(t, g, "v1", func(target string) (string, error) {
		installedMu.Lock()
		installedVersions[ok.cfg.NodeID] = target
		installedMu.Unlock()
		return target, nil
	})
	// bad: the download fails (checksum mismatch): reports the reason, keeps running v1.
	bad := startUpNode(t, g, "v1", func(string) (string, error) {
		return "", errors.New("checksum mismatch for agent-net-linux-amd64")
	})
	// same: already runs the target, nothing to do.
	same := startUpNode(t, g, "v2", func(target string) (string, error) { return target, nil })
	// old: a daemon from before remote upgrade.
	old := startUpNode(t, g, "step2", nil)
	// off: connected once, now stopped.
	off := startUpNode(t, g, "v1", nil)
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

	res, err := g.gw.UpgradeNodes(context.Background(), "v2", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	got := byNode(res)
	if len(got) != 5 {
		t.Fatalf("want 5 results, got %+v", res)
	}
	if r := got[ok.cfg.NodeID]; r.Status != protocol.UpgradeOK || r.From != "v1" || r.To != "v2" {
		t.Errorf("ok node: %+v", r)
	}
	if r := got[bad.cfg.NodeID]; r.Status != protocol.UpgradeFailed || !strings.Contains(r.Detail, "checksum mismatch") || r.From != "v1" {
		t.Errorf("bad node: %+v", r)
	}
	if r := got[same.cfg.NodeID]; r.Status != protocol.UpgradeOK || r.To != "v2" {
		t.Errorf("same node: %+v", r)
	}
	if r := got[old.cfg.NodeID]; r.Status != protocol.UpgradeFailed || !strings.Contains(r.Detail, "too old") {
		t.Errorf("old node: %+v", r)
	}
	if r := got[off.cfg.NodeID]; r.Status != protocol.UpgradeOffline || !strings.Contains(r.Detail, "upgrades itself") {
		t.Errorf("offline node: %+v", r)
	}

	// The gateway stored the version the restarted daemon reported.
	if v, _ := g.gw.Store().NodeVersion(ok.cfg.NodeID); v != "v2" {
		t.Errorf("stored version of the upgraded node is %q", v)
	}
	if v, _ := g.gw.Store().NodeVersion(bad.cfg.NodeID); v != "v1" {
		t.Errorf("stored version of the failed node is %q", v)
	}
}

func TestNodeReportsVersionLocally(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	a := startUpNode(t, g, "v1.2.3", nil)
	b := startUpNode(t, g, "v1.2.4", nil)
	cli := dialLocal(t, a.cfg.SocketPath)
	raw := cli.mustCall("hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol})
	if !strings.Contains(string(raw), `"version":"v1.2.3"`) {
		t.Fatalf("hello reply %s lacks the version", raw)
	}
	var info protocol.NodesInfo
	if err := json.Unmarshal(cli.mustCall("nodes", nil), &info); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, n := range info.Nodes {
		if n.Online {
			seen[n.NodeID] = n.Version
		}
	}
	if seen[a.cfg.NodeID] != "v1.2.3" || seen[b.cfg.NodeID] != "v1.2.4" {
		t.Fatalf("nodes = %+v", info.Nodes)
	}
	if info, err := json.Marshal(cli.mustCall("info", nil)); err != nil || !strings.Contains(string(info), "v1.2.3") {
		t.Fatalf("info lacks the version: %s", info)
	}
}

func TestOldLocalClientGetsRestartSessionError(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	// A future release that dropped compatibility with protocol 0 clients.
	n := startUpNode(t, g, "v2", nil, func(c *Config) { c.MinLocalProtocol = 1 })

	old := dialLocal(t, n.cfg.SocketPath)
	_, err := old.call("hello", map[string]any{"client": "cli"})
	if err == nil || !strings.Contains(err.Error(), "restart this session") {
		t.Fatalf("want a 'restart this session' error, got %v", err)
	}
	cur := dialLocal(t, n.cfg.SocketPath)
	if _, err := cur.call("hello", map[string]any{"client": "cli", "protocol": 1}); err != nil {
		t.Fatalf("a current client must be served: %v", err)
	}
}

func TestOldClientsAreServedByDefault(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startUpNode(t, g, "v2", nil)
	old := dialLocal(t, n.cfg.SocketPath)
	if _, err := old.call("hello", map[string]any{"client": "cli"}); err != nil {
		t.Fatalf("a client without protocol must still work within a release: %v", err)
	}
}

func TestRestartIsForTheCLIOnly(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	restarted := make(chan struct{}, 1)
	n := startUpNode(t, g, "v2", nil, func(c *Config) { c.Restart = func() { restarted <- struct{}{} } })
	pi := dialLocal(t, n.cfg.SocketPath)
	pi.mustCall("hello", map[string]any{"client": "pi", "harness": "pi", "native_id": "x", "cwd": "/w"})
	if _, err := pi.call("restart", nil); err == nil {
		t.Fatal("a plugin client must not restart the daemon")
	}
	cli := dialLocal(t, n.cfg.SocketPath)
	cli.mustCall("hello", map[string]any{"client": "cli"})
	cli.mustCall("restart", nil)
	select {
	case <-restarted:
	case <-time.After(scaled(3 * time.Second)):
		t.Fatal("restart was not called")
	}
}
