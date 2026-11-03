package gateway

import (
	"net"
	"testing"
)

func TestAddressClassificationTable(t *testing.T) {
	tests := []struct {
		ip       string
		wantKind IPKind
		isPublic bool
	}{
		// Public IPv4
		{"8.8.8.8", IPKindPublic, true},
		{"1.1.1.1", IPKindPublic, true},
		{"93.184.216.34", IPKindPublic, true},
		{"100.63.255.255", IPKindPublic, true},
		{"100.128.0.1", IPKindPublic, true},

		// Public IPv6
		{"2001:4860:4860::8888", IPKindPublic, true},
		{"2606:4700:4700::1111", IPKindPublic, true},

		// RFC1918 Private IPv4
		{"10.0.0.1", IPKindPrivate, false},
		{"10.255.255.254", IPKindPrivate, false},
		{"172.16.0.1", IPKindPrivate, false},
		{"172.31.255.254", IPKindPrivate, false},
		{"192.168.0.1", IPKindPrivate, false},
		{"192.168.1.100", IPKindPrivate, false},

		// CGNAT (100.64.0.0/10 used by Tailscale)
		{"100.64.0.1", IPKindCGNAT, false},
		{"100.100.100.100", IPKindCGNAT, false},
		{"100.127.255.254", IPKindCGNAT, false},

		// Loopback
		{"127.0.0.1", IPKindLoopback, false},
		{"127.0.0.2", IPKindLoopback, false},
		{"::1", IPKindLoopback, false},

		// IPv6 ULA (fc00::/7)
		{"fc00::1", IPKindIPv6ULA, false},
		{"fd00::1", IPKindIPv6ULA, false},
		{"fd12:3456:789a::1", IPKindIPv6ULA, false},

		// Link-local
		{"169.254.1.1", IPKindLinkLocal, false},
		{"fe80::1", IPKindLinkLocal, false},

		// Unspecified
		{"0.0.0.0", IPKindUnspecified, false},
		{"::", IPKindUnspecified, false},
	}

	for _, tc := range tests {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("failed to parse IP %q", tc.ip)
		}
		gotKind := ClassifyIP(ip)
		if gotKind != tc.wantKind {
			t.Errorf("ClassifyIP(%s) = %v, want %v", tc.ip, gotKind, tc.wantKind)
		}
		gotPublic := IsPublicIP(ip)
		if gotPublic != tc.isPublic {
			t.Errorf("IsPublicIP(%s) = %v, want %v", tc.ip, gotPublic, tc.isPublic)
		}
	}
}
