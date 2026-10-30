package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// Joining again keeps the ssh settings set by hand, including false.
func TestJoinKeepsEnterSSH(t *testing.T) {
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
