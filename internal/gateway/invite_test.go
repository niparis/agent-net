package gateway

import (
	"strings"
	"testing"
)

func TestInviteLinesFromGatewayData(t *testing.T) {
	dir := seededDataDir(t)
	gw, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer gw.Close()
	lines, err := Invite(InviteOptions{Host: "127.0.0.1", Port: "7420", Data: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines: %+v", lines)
	}
	if !strings.Contains(lines[0][1], "curl -fsSL -k --pinnedpubkey") || !strings.Contains(lines[0][1], "https://127.0.0.1:7420/install/") {
		t.Fatalf("invite line: %q", lines[0][1])
	}
}

func TestInviteNeedsStartedGateway(t *testing.T) {
	if _, err := Invite(InviteOptions{Host: "127.0.0.1", Port: "7420", Data: t.TempDir()}); err == nil {
		t.Fatal("invite worked without a gateway")
	}
}

func TestIsTailscale(t *testing.T) {
	if isTailscale("192.168.1.2") || !isTailscale("100.64.0.10") || isTailscale("100.128.0.1") {
		t.Error("tailscale range check")
	}
}
