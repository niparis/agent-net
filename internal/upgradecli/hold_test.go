package upgradecli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/upgrade"
)

// isolate points every path the command may write at a temp dir.
func isolate(t *testing.T) (state string) {
	t.Helper()
	home := t.TempDir()
	state = filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("AGENT_NET_STATE_DIR", state)
	t.Setenv("AGENT_NET_CONFIG_DIR", filepath.Join(home, "config"))
	t.Setenv("AGENT_NET_SOCKET", filepath.Join(home, "d.sock"))
	t.Setenv("AGENT_NET_NO_SERVICE", "1")
	return state
}

func script(version string) []byte { return []byte("#!/bin/sh\necho " + version + "\n") }

func TestRollbackHoldsTheVersionItLeft(t *testing.T) {
	state := isolate(t)
	exe := filepath.Join(t.TempDir(), "agent-net")
	os.WriteFile(exe, script("v0.0.2"), 0o755)
	os.WriteFile(upgrade.PrevPath(exe), script("v0.0.1"), 0o755)
	var out bytes.Buffer
	err := Run(context.Background(), Options{Current: "v0.0.2", Exe: exe, GatewayData: gatewayDataDir(t), Rollback: true, Out: &out})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if h := upgrade.LoadAuto(state).Hold; h != "v0.0.2" {
		t.Fatalf("hold = %q, want v0.0.2\n%s", h, out.String())
	}
	if !strings.Contains(out.String(), "held at v0.0.2") {
		t.Errorf("rollback does not say it holds:\n%s", out.String())
	}

	// --check shows the hold.
	t.Setenv("AGENT_NET_RELEASE_BASE_URL", "http://127.0.0.1:1")
	out.Reset()
	if err := Run(context.Background(), Options{Current: "v0.0.1", Exe: exe, GatewayData: gatewayDataDir(t), Check: true, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "auto-upgrade   held at v0.0.2 after rollback") {
		t.Errorf("--check lacks the hold:\n%s", out.String())
	}
}

func TestRollbackFromADevBuildHoldsNothing(t *testing.T) {
	state := isolate(t)
	exe := filepath.Join(t.TempDir(), "agent-net")
	os.WriteFile(exe, script("dev"), 0o755)
	os.WriteFile(upgrade.PrevPath(exe), script("v0.0.1"), 0o755)
	if err := Run(context.Background(), Options{Current: "dev", Exe: exe, GatewayData: gatewayDataDir(t), Rollback: true, Out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if h := upgrade.LoadAuto(state).Hold; h != "" {
		t.Fatalf("hold = %q for a dev build", h)
	}
}

func TestManualUpgradeClearsTheHold(t *testing.T) {
	state := isolate(t)
	// A release server with v0.0.3.
	files := map[string][]byte{}
	var sums strings.Builder
	for _, name := range upgrade.AllAssets() {
		files[name] = script("v0.0.3")
		s := sha256.Sum256(files[name])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(s[:]), name)
	}
	files["checksums.txt"] = []byte(sums.String())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[filepath.Base(r.URL.Path)]
		if !ok || !strings.HasPrefix(r.URL.Path, "/download/v0.0.3/") {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	defer srv.Close()

	if err := upgrade.SetHold(state, "v0.0.2"); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "agent-net")
	os.WriteFile(exe, script("v0.0.1"), 0o755)
	// The gateway is not running, so the run stops after the install, when it
	// waits for the gateway. A short context ends that wait.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out bytes.Buffer
	Run(ctx, Options{Current: "v0.0.1", Exe: exe, GatewayData: gatewayDataDir(t), Version: "v0.0.3", Base: srv.URL, Out: &out})
	if v, _ := upgrade.BinaryVersion(context.Background(), exe); v != "v0.0.3" {
		t.Fatalf("the binary is %s, the upgrade did not run:\n%s", v, out.String())
	}
	if h := upgrade.LoadAuto(state).Hold; h != "" {
		t.Fatalf("hold = %q after a manual upgrade", h)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".local", "share", "agent-net", "dist", "agent-net-linux-amd64")); err != nil {
		t.Errorf("dist was not written under the test HOME: %v", err)
	}
}
