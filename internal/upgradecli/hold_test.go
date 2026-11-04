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
	Run(ctx, Options{Current: "v0.0.1", Exe: exe, GatewayData: gatewayDataDir(t), Version: "v0.0.3", Base: srv.URL, Out: &out,
		download: func(_ context.Context, _ *http.Client, _ string, _ []string, dir string) error {
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o755); err != nil {
					return err
				}
			}
			return os.WriteFile(filepath.Join(dir, "checksums.txt.sig"), []byte("test signature"), 0o644)
		}})
	if v, _ := upgrade.BinaryVersion(context.Background(), exe); v != "v0.0.3" {
		t.Fatalf("the binary is %s, the upgrade did not run:\n%s", v, out.String())
	}
	if h := upgrade.LoadAuto(state).Hold; h != "" {
		t.Fatalf("hold = %q after a manual upgrade", h)
	}
	for _, name := range []string{"agent-net-linux-amd64", "checksums.txt", "checksums.txt.sig"} {
		if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".local", "share", "agent-net", "dist", name)); err != nil {
			t.Errorf("%s was not written under the test HOME: %v", name, err)
		}
	}
}

func TestGatewayRollbackInvalidatesManifestBeforeOldSetup(t *testing.T) {
	isolate(t)
	dist := filepath.Join(os.Getenv("HOME"), ".local", "share", "agent-net", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"checksums.txt", "checksums.txt.sig"} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte("old metadata"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(t.TempDir(), "agent-net")
	if err := os.WriteFile(exe, script("v0.0.2"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = setup ]; then\n  [ ! -e %q ] && [ ! -e %q ] || exit 1\n  exit 0\nfi\necho v0.0.1\n", filepath.Join(dist, "checksums.txt"), filepath.Join(dist, "checksums.txt.sig"))
	if err := os.WriteFile(upgrade.PrevPath(exe), []byte(old), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Run(context.Background(), Options{Current: "v0.0.2", Exe: exe, GatewayData: gatewayDataDir(t), Rollback: true, Out: &out}); err != nil {
		t.Fatalf("old setup observed a stale manifest: %v\n%s", err, out.String())
	}
}

func TestUpgradeInvalidatesIncompleteDist(t *testing.T) {
	otherAsset := "agent-net-linux-amd64"
	if otherAsset == upgrade.ThisAsset() {
		otherAsset = "agent-net-darwin-arm64"
	}
	for _, missing := range []string{otherAsset, "checksums.txt", "checksums.txt.sig"} {
		t.Run(missing, func(t *testing.T) {
			isolate(t)
			dist := filepath.Join(os.Getenv("HOME"), ".local/share/agent-net/dist")
			if err := os.MkdirAll(dist, 0755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"checksums.txt", "checksums.txt.sig"} {
				if err := os.WriteFile(filepath.Join(dist, name), []byte("old"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			exe := filepath.Join(t.TempDir(), "agent-net")
			if err := os.WriteFile(exe, script("v0.0.1"), 0755); err != nil {
				t.Fatal(err)
			}
			r := &run{Options: Options{Current: "v0.0.1", Exe: exe, GatewayData: gatewayDataDir(t), Out: &bytes.Buffer{}, download: func(_ context.Context, _ *http.Client, _ string, _ []string, dir string) error {
				for _, name := range append(upgrade.AllAssets(), upgrade.ReleaseMetadata...) {
					if name == missing {
						continue
					}
					data := []byte("metadata")
					if strings.HasPrefix(name, "agent-net-") {
						data = script("v0.0.2")
					}
					if err := os.WriteFile(filepath.Join(dir, name), data, 0755); err != nil {
						return err
					}
				}
				return nil
			}}}
			if err := r.installRelease(context.Background(), "v0.0.2"); err == nil {
				t.Fatal("expected copy failure")
			}
			for _, name := range []string{"checksums.txt", "checksums.txt.sig"} {
				if _, err := os.Stat(filepath.Join(dist, name)); !os.IsNotExist(err) {
					t.Errorf("%s remains: %v", name, err)
				}
			}
		})
	}
}

func TestGatewayManualDowngradeRequiresForce(t *testing.T) {
	isolate(t)
	exe := filepath.Join(t.TempDir(), "agent-net")
	if err := os.WriteFile(exe, script("v0.0.3"), 0755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	opts := Options{Current: "v0.0.3", Exe: exe, GatewayData: gatewayDataDir(t), Version: "v0.0.2", Base: "http://127.0.0.1:1", Out: &bytes.Buffer{}, download: func(_ context.Context, _ *http.Client, _ string, _ []string, dir string) error {
		calls++
		return fmt.Errorf("reached download")
	}}
	err := Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "--force") || calls != 0 {
		t.Fatalf("downgrade: %v, downloads %d", err, calls)
	}
	opts.Force = true
	err = Run(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "reached download") || calls != 1 {
		t.Fatalf("forced downgrade did not reach download: %v, calls %d", err, calls)
	}
}

func TestDistFromArgs(t *testing.T) {
	for _, args := range [][]string{nil, {"--gateway"}, {"--dist"}, {"--dist", "--port"}, {"--dist", ""}} {
		if _, err := distFromArgs(args); err == nil {
			t.Errorf("accepted invalid args %q", args)
		}
	}
	if got, err := distFromArgs([]string{"--gateway", "--dist", "/tmp/dist"}); err != nil || got != "/tmp/dist" {
		t.Fatalf("dist = %q, %v", got, err)
	}
}
