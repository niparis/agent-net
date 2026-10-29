package upgradecli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestFormatResult(t *testing.T) {
	cases := []struct {
		res  protocol.UpgradeResult
		want string
	}{
		{protocol.UpgradeResult{Hostname: "mac", NodeID: "n_1", From: "v0.1.0", To: "v0.2.0", Status: protocol.UpgradeOK},
			"mac n_1  v0.1.0 -> v0.2.0  ok"},
		{protocol.UpgradeResult{Hostname: "box", NodeID: "n_2", From: "v0.1.0", To: "v0.2.0", Status: protocol.UpgradeFailed, Detail: "checksum mismatch"},
			"box n_2  v0.1.0 -> v0.2.0  failed (checksum mismatch)"},
		{protocol.UpgradeResult{Hostname: "pi", NodeID: "n_3", From: "step2", To: "v0.2.0", Status: protocol.UpgradeOffline,
			Detail: "upgrades itself when it reconnects, unless auto-upgrade is turned off there"},
			"pi n_3  unknown -> v0.2.0  offline (upgrades itself when it reconnects, unless auto-upgrade is turned off there)"},
		{protocol.UpgradeResult{Hostname: "vm", NodeID: "n_4", From: "v0.1.0", To: "v0.2.0", Status: protocol.UpgradeOK, Detail: "upgraded on reconnect"},
			"vm n_4  v0.1.0 -> v0.2.0  ok (upgraded on reconnect)"},
	}
	for _, c := range cases {
		if got := FormatResult(c.res); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestSessionsNotesMatchCurrentBehavior(t *testing.T) {
	var out bytes.Buffer
	(&run{Options: Options{Out: &out}}).saySessions()
	got := out.String()
	for _, want := range []string{"Claude Code (started on v0.3.2 or later)", "Pi: run /reload", "OpenCode: reloads the plugin file by itself"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "keep the old version") {
		t.Errorf("outdated note still printed:\n%s", got)
	}
}

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.1.0", false},
		{"v0.2.0", "v0.2.0", false},
		{"v0.9.0", "v0.10.0", true},
		{"dev", "v0.2.0", true},
	} {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func gatewayDataDir(t *testing.T) string {
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "ca.pem"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDevBuildRefusesWithoutForce(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "agent-net")
	os.WriteFile(exe, []byte("#!/bin/sh\necho dev\n"), 0o755)
	// The base URL is unreachable on purpose: the dev check must come first
	// for anything that would change the machine.
	t.Setenv("AGENT_NET_RELEASE_BASE_URL", "http://127.0.0.1:1")
	var out bytes.Buffer
	err := Run(context.Background(), Options{Current: "dev", Exe: exe, GatewayData: gatewayDataDir(t), Out: &out})
	if err == nil || !strings.Contains(err.Error(), "dev build") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want the dev build refusal, got %v", err)
	}
}

func TestCheckChangesNothing(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "agent-net")
	os.WriteFile(exe, []byte("#!/bin/sh\necho v1\n"), 0o755)
	t.Setenv("AGENT_NET_RELEASE_BASE_URL", "http://127.0.0.1:1")
	var out bytes.Buffer
	data := gatewayDataDir(t)
	if err := Run(context.Background(), Options{Current: "v1", Exe: exe, GatewayData: data, Check: true, Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "this machine   v1") || !strings.Contains(out.String(), "not reachable") {
		t.Fatalf("unexpected --check output:\n%s", out.String())
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Fatalf("--check wrote files: %v", entries)
	}
}

func TestNotSetUp(t *testing.T) {
	t.Setenv("AGENT_NET_CONFIG_DIR", t.TempDir())
	err := Run(context.Background(), Options{Current: "v1", Exe: "/nonexistent", GatewayData: t.TempDir(), Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "not set up") {
		t.Fatalf("got %v", err)
	}
}
