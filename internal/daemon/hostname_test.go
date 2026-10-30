package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Joining again keeps a host name that was set by hand in node.json.
func TestJoinKeepsPinnedHostname(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "node.json"), []byte(`{"hostname":"mac-mini"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	nc, err := Join(context.Background(), g.joinString(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Hostname != "mac-mini" {
		t.Fatalf("returned config: %+v", nc)
	}
	got, _, err := LoadNode(dir)
	if err != nil || got.Hostname != "mac-mini" {
		t.Fatalf("node.json: %+v %v", got, err)
	}
}

func TestDisplayHostname(t *testing.T) {
	scutil := func(out string, err error) func(string) (string, error) {
		return func(string) (string, error) { return out, err }
	}
	cases := []struct {
		name, goos, osName, pinned string
		scutil                     func(string) (string, error)
		want                       string
	}{
		// The bug: the same Mac reports different os.Hostname() values per network.
		{"mac short", "darwin", "Laptop-1.local", "", scutil("Laptop1\n", nil), "Laptop1"},
		{"mac other network", "darwin", "Laptop1", "", scutil("Laptop1", nil), "Laptop1"},
		{"mac scutil fails, .local", "darwin", "Laptop-1.local", "", scutil("", errors.New("x")), "Laptop-1"},
		{"mac scutil fails, domain", "darwin", "Laptop-1.example.net", "", scutil("", errors.New("x")), "Laptop-1"},
		{"mac scutil empty", "darwin", "Laptop1", "", scutil("  ", nil), "Laptop1"},
		{"pinned wins", "darwin", "Laptop-1.local", "mac-mini", scutil("Laptop1", nil), "mac-mini"},
		{"pinned on linux", "linux", "build", "ci-1", nil, "ci-1"},
		// Linux keeps os.Hostname() as it is, even with a dot.
		{"linux plain", "linux", "build-box", "", nil, "build-box"},
		{"linux fqdn kept", "linux", "build.example.net", "", nil, "build.example.net"},
		{"empty", "linux", "", "", nil, ""},
	}
	for _, c := range cases {
		got := displayHostname(c.pinned, c.goos, func() (string, error) { return c.osName, nil }, c.scutil)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
