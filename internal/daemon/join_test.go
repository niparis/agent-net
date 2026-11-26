package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// enter_ssh is on unless node.json says false. An unset field must not be
// mistaken for false (decision 0002, W16).
func TestEnterSSHIsOnUnlessNodeConfigSaysFalse(t *testing.T) {
	for _, c := range []struct {
		name, raw string
		want      bool
	}{
		{"unset", `{}`, true},
		{"true", `{"enter_ssh":true}`, true},
		{"false", `{"enter_ssh":false}`, false},
	} {
		var nc NodeConfig
		if err := json.Unmarshal([]byte(c.raw), &nc); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := nc.EnterSSHEnabled(); got != c.want {
			t.Errorf("%s: enter_ssh is %v, want %v", c.name, got, c.want)
		}
	}
}

// Joining again keeps an explicit harness path set by hand.
func TestJoinKeepsHarnessPaths(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	dir := t.TempDir()
	const herdr = "/opt/herdr/bin/herdr"
	raw := `{"harness_paths":{"herdr":"` + herdr + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "node.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	nc, err := Join(context.Background(), g.joinString(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := nc.HarnessPaths["herdr"]; got != herdr {
		t.Fatalf("returned config: %+v", nc)
	}
	got, _, err := LoadNode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.HarnessPaths["herdr"] != herdr {
		t.Fatalf("node.json: %+v", got)
	}
}

// Joining again keeps the ssh settings set by hand, including false.
func TestJoinKeepsEnterSSH(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	dir := t.TempDir()
	raw := `{"enter_ssh":false,"enter_ssh_hosts":{"box":"me@box"}}`
	if err := os.WriteFile(filepath.Join(dir, "node.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	nc, err := Join(context.Background(), g.joinString(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if nc.EnterSSHEnabled() || nc.EnterSSHHosts["box"] != "me@box" {
		t.Fatalf("returned config: %+v", nc)
	}
	got, _, err := LoadNode(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.EnterSSHEnabled() || got.EnterSSHHosts["box"] != "me@box" {
		t.Fatalf("node.json: %+v", got)
	}
}

// A gateway that moves to a new address keeps this machine's identity when the
// new address serves the same CA and accepts the same token.
func TestChangeGatewayKeepsNodeIdentity(t *testing.T) {
	t.Parallel()
	data := t.TempDir()
	g := startGateway(t, data, "")
	cfg := t.TempDir()
	joined, err := Join(context.Background(), g.joinString(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.stop()
	moved := startGateway(t, data, "")
	want := "https://" + moved.addr
	old, got, err := ChangeGateway(context.Background(), want+"/", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if old != "https://"+g.addr || got != want {
		t.Fatalf("change = %q -> %q, want %q -> %q", old, got, "https://"+g.addr, want)
	}
	nc, _, err := LoadNode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if nc.GatewayURL != want {
		t.Fatalf("gateway_url = %q, want %q", nc.GatewayURL, want)
	}
	if nc.NodeID != joined.NodeID || nc.Token != joined.Token {
		t.Fatalf("identity changed: %+v, want node %s and the same token", nc, joined.NodeID)
	}
}

// A new address with a different CA changes nothing.
func TestChangeGatewayRefusesDifferentCA(t *testing.T) {
	t.Parallel()
	g := startGateway(t, t.TempDir(), "")
	cfg := t.TempDir()
	if _, err := Join(context.Background(), g.joinString(), cfg); err != nil {
		t.Fatal(err)
	}
	before := readNodeJSON(t, cfg)
	other := startGateway(t, t.TempDir(), "")
	_, _, err := ChangeGateway(context.Background(), "https://"+other.addr, cfg)
	if err == nil || !strings.Contains(err.Error(), "different CA") {
		t.Fatalf("want a different CA error, got %v", err)
	}
	if after := readNodeJSON(t, cfg); string(after) != string(before) {
		t.Fatalf("node.json changed after a refused CA:\n%s", after)
	}
}

// The same CA at a new address is not enough: the gateway must also know this
// machine's token, and a refused token changes nothing.
func TestChangeGatewayRefusesUnknownToken(t *testing.T) {
	t.Parallel()
	data := t.TempDir()
	g := startGateway(t, data, "")
	cfg := t.TempDir()
	if _, err := Join(context.Background(), g.joinString(), cfg); err != nil {
		t.Fatal(err)
	}
	g.stop()
	// Same keys, new database: the CA pin passes and the token is unknown.
	otherData := t.TempDir()
	for _, name := range []string{"ca.pem", "ca.key", "server-key.pem"} {
		raw, err := os.ReadFile(filepath.Join(data, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(otherData, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	other := startGateway(t, otherData, "")
	before := readNodeJSON(t, cfg)
	_, _, err := ChangeGateway(context.Background(), "https://"+other.addr, cfg)
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("want a refused token error, got %v", err)
	}
	if after := readNodeJSON(t, cfg); string(after) != string(before) {
		t.Fatalf("node.json changed after a refused token:\n%s", after)
	}
}

func readNodeJSON(t *testing.T, dir string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "node.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
