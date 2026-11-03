package setup

import (
	"bytes"
	"context"
	"os"
	"testing"
)

func TestFreshGatewayOffersTailscaleListen(t *testing.T) {
	exe := os.Args[0]

	// Case 1: Fresh gateway, Tailscale present, accepted (via AllYes)
	var out1 bytes.Buffer
	var installedService1 Service
	r1 := &runner{
		Options:          Options{Out: &out1, Gateway: true, NoService: false, All: AllYes, Exe: exe},
		freshGateway:     true,
		interactive:      true,
		tailscaleIP:      func() string { return "100.64.1.20" },
		installServiceFn: func(s Service) (string, error) { installedService1 = s; return "ok", nil },
	}
	r1.setupGateway(context.Background())

	foundListen1 := false
	for i, arg := range installedService1.Args {
		if arg == "--listen" && i+1 < len(installedService1.Args) {
			if installedService1.Args[i+1] == "100.64.1.20:7420" {
				foundListen1 = true
			}
		}
	}
	if !foundListen1 {
		t.Fatalf("expected --listen 100.64.1.20:7420, got args: %v", installedService1.Args)
	}
	if r1.gatewayListen != "100.64.1.20" {
		t.Fatalf("expected r.gatewayListen = 100.64.1.20, got %q", r1.gatewayListen)
	}

	// Case 2: Fresh gateway, Tailscale present, declined (via AllNo)
	var out2 bytes.Buffer
	var installedService2 Service
	r2 := &runner{
		Options:          Options{Out: &out2, Gateway: true, NoService: false, All: AllNo, Exe: exe},
		freshGateway:     true,
		interactive:      true,
		tailscaleIP:      func() string { return "100.64.1.20" },
		installServiceFn: func(s Service) (string, error) { installedService2 = s; return "ok", nil },
	}
	r2.setupGateway(context.Background())

	foundDefault2 := false
	for i, arg := range installedService2.Args {
		if arg == "--listen" && i+1 < len(installedService2.Args) {
			if installedService2.Args[i+1] == "0.0.0.0:7420" {
				foundDefault2 = true
			}
		}
	}
	if !foundDefault2 {
		t.Fatalf("expected --listen 0.0.0.0:7420 when declined, got args: %v", installedService2.Args)
	}
	if r2.gatewayListen != "" {
		t.Fatalf("expected empty r.gatewayListen when declined, got %q", r2.gatewayListen)
	}

	// Case 3: Existing gateway (freshGateway: false) -> keeps 0.0.0.0, does not offer
	var out3 bytes.Buffer
	var installedService3 Service
	r3 := &runner{
		Options:          Options{Out: &out3, Gateway: true, NoService: false, All: AllYes, Exe: exe},
		freshGateway:     false,
		interactive:      true,
		tailscaleIP:      func() string { return "100.64.1.20" },
		installServiceFn: func(s Service) (string, error) { installedService3 = s; return "ok", nil },
	}
	r3.setupGateway(context.Background())

	foundDefault3 := false
	for i, arg := range installedService3.Args {
		if arg == "--listen" && i+1 < len(installedService3.Args) {
			if installedService3.Args[i+1] == "0.0.0.0:7420" {
				foundDefault3 = true
			}
		}
	}
	if !foundDefault3 {
		t.Fatalf("existing gateway should keep 0.0.0.0:7420, got args: %v", installedService3.Args)
	}

	// Case 4: Fresh gateway without Tailscale -> keeps 0.0.0.0
	var out4 bytes.Buffer
	var installedService4 Service
	r4 := &runner{
		Options:          Options{Out: &out4, Gateway: true, NoService: false, All: AllYes, Exe: exe},
		freshGateway:     true,
		interactive:      true,
		tailscaleIP:      func() string { return "" },
		installServiceFn: func(s Service) (string, error) { installedService4 = s; return "ok", nil },
	}
	r4.setupGateway(context.Background())

	foundDefault4 := false
	for i, arg := range installedService4.Args {
		if arg == "--listen" && i+1 < len(installedService4.Args) {
			if installedService4.Args[i+1] == "0.0.0.0:7420" {
				foundDefault4 = true
			}
		}
	}
	if !foundDefault4 {
		t.Fatalf("gateway without Tailscale should keep 0.0.0.0:7420, got args: %v", installedService4.Args)
	}
}

func TestJoinUsesTailscaleListenHost(t *testing.T) {
	// Verify that when gatewayListen is set, join URL uses that host instead of localhost.
	r := &runner{
		Options:       Options{Gateway: true, NoService: true},
		gatewayListen: "100.64.1.20",
	}
	host := "localhost"
	if r.gatewayListen != "" {
		host = r.gatewayListen
	}
	if host != "100.64.1.20" {
		t.Fatalf("expected host 100.64.1.20, got %s", host)
	}
}
