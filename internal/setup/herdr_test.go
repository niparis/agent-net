package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHerdr writes a stand-in binary that answers the version probe.
func fakeHerdr(t *testing.T, dir, version string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "herdr")
	body := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'herdr " + version + "'; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// stubHerdrInstall replaces the official installer and says whether it ran.
func stubHerdrInstall(t *testing.T, fn func(context.Context) error) *bool {
	t.Helper()
	called := false
	old := installHerdr
	installHerdr = func(ctx context.Context) error {
		called = true
		return fn(ctx)
	}
	t.Cleanup(func() { installHerdr = old })
	return &called
}

func TestSetupFindsHerdr(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	bin := fakeHerdr(t, dir, "0.9.5")
	called := stubHerdrInstall(t, func(context.Context) error { return nil })

	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "herdr ok (found "+displayPath(bin)+")") {
		t.Errorf("output lacks the herdr line:\n%s", out)
	}
	if *called {
		t.Error("setup ran the installer while herdr was present")
	}
}

func TestSetupInstallsMissingHerdr(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	dir := filepath.Join(f.home, ".local", "bin")
	called := stubHerdrInstall(t, func(context.Context) error {
		fakeHerdr(t, dir, "0.9.5")
		return nil
	})

	out := run(t, Options{Join: f.newJoin(t)})
	if !*called {
		t.Fatal("setup did not run the installer while herdr was missing")
	}
	if !strings.Contains(out, "herdr installed (0.9.5, "+displayPath(filepath.Join(dir, "herdr"))+")") {
		t.Errorf("output lacks the installed line:\n%s", out)
	}
}

func TestSetupHerdrInstallFailureContinues(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	stubHerdrInstall(t, func(context.Context) error {
		return errors.New("curl could not reach herdr.dev")
	})

	out := run(t, Options{Join: f.newJoin(t)})
	if !strings.Contains(out, "herdr failed (curl could not reach herdr.dev; install it from https://herdr.dev/tui/docs/install/)") {
		t.Errorf("output lacks the failed line:\n%s", out)
	}
	if !strings.Contains(out, "agent guidance") {
		t.Errorf("setup stopped after the herdr failure:\n%s", out)
	}
}

func TestSetupNoHerdrSkipsInstall(t *testing.T) {
	f := newFixture(t)
	t.Setenv("PATH", t.TempDir())
	called := stubHerdrInstall(t, func(context.Context) error { return nil })

	out := run(t, Options{Join: f.newJoin(t), NoHerdr: true})
	if !strings.Contains(out, "herdr skipped (--no-herdr)") {
		t.Errorf("output lacks the skipped line:\n%s", out)
	}
	if *called {
		t.Error("setup ran the installer with --no-herdr")
	}
}

func TestSetupHerdrUsesNodeConfiguredPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	cfg := filepath.Join(home, "cfg")
	t.Setenv("AGENT_NET_CONFIG_DIR", cfg)
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := fakeHerdr(t, t.TempDir(), "0.9.5")
	body := `{"node_id":"n_test","harness_paths":{"herdr":"` + bin + `"}}`
	if err := os.WriteFile(filepath.Join(cfg, "node.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	called := stubHerdrInstall(t, func(context.Context) error { return nil })

	var out bytes.Buffer
	r, err := newRunner(Options{Out: &out, Exe: "/opt/test/agent-net"})
	if err != nil {
		t.Fatal(err)
	}
	r.herdr(context.Background())
	if !strings.Contains(norm(out.String()), "herdr ok (found "+displayPath(bin)+")") {
		t.Errorf("output lacks the configured line:\n%s", out.String())
	}
	if *called {
		t.Error("setup ran the installer while harness_paths.herdr was set")
	}
}
