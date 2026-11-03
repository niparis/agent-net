package gateway

import (
	"net"
	"strings"
	"testing"
)

func TestExposureWarningAppearsWithPublicAddressAndNotWithout(t *testing.T) {
	// Case 1: listener has a public address -> warning appears
	gwPublic, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer gwPublic.Close()

	gwPublic.CheckExposureWithAddrs("203.0.113.1:7420", func() ([]net.Addr, error) {
		return nil, nil
	})

	warnings := gwPublic.ExposureWarnings()
	if len(warnings) == 0 {
		t.Fatal("expected exposure warning for public address, got none")
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "gateway reachable on public address 203.0.113.1") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warning mentioning 203.0.113.1, got %v", warnings)
	}

	info, err := gwPublic.NodesInfo()
	if err != nil {
		t.Fatal(err)
	}
	if len(info.GatewayWarnings) == 0 || !strings.Contains(info.GatewayWarnings[0], "gateway reachable on public address 203.0.113.1") {
		t.Fatalf("NodesInfo.GatewayWarnings = %v, want warning", info.GatewayWarnings)
	}

	// Case 2: listener has a private / CGNAT / loopback address -> no warning
	gwPrivate, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer gwPrivate.Close()

	for _, addr := range []string{"192.168.1.50:7420", "10.0.0.5:7420", "100.64.0.1:7420", "127.0.0.1:7420", "[::1]:7420", "[fd00::1]:7420"} {
		gwPrivate.CheckExposureWithAddrs(addr, func() ([]net.Addr, error) {
			return nil, nil
		})
		if w := gwPrivate.ExposureWarnings(); len(w) != 0 {
			t.Fatalf("addr %s produced unexpected warnings: %v", addr, w)
		}
		info, err := gwPrivate.NodesInfo()
		if err != nil {
			t.Fatal(err)
		}
		if len(info.GatewayWarnings) != 0 {
			t.Fatalf("addr %s produced unexpected NodesInfo.GatewayWarnings: %v", addr, info.GatewayWarnings)
		}
	}

	// Case 3: 0.0.0.0 with public interface vs without public interface
	gwAll, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer gwAll.Close()

	// 0.0.0.0 with only private + loopback interfaces -> no warning
	gwAll.CheckExposureWithAddrs("0.0.0.0:7420", func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("192.168.1.10"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("100.80.1.2"), Mask: net.CIDRMask(10, 32)},
		}, nil
	})
	if w := gwAll.ExposureWarnings(); len(w) != 0 {
		t.Fatalf("0.0.0.0 with private interfaces produced warning: %v", w)
	}

	// 0.0.0.0 with a public interface -> warning appears
	gwAll.CheckExposureWithAddrs("0.0.0.0:7420", func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("198.51.100.25"), Mask: net.CIDRMask(24, 32)},
		}, nil
	})
	w := gwAll.ExposureWarnings()
	if len(w) == 0 || !strings.Contains(w[0], "gateway reachable on public address 198.51.100.25") {
		t.Fatalf("0.0.0.0 with public interface expected warning, got: %v", w)
	}
}
