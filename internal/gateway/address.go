package gateway

import (
	"fmt"
	"net"
	"strings"
)

// IPKind identifies the classification of an IP address.
type IPKind int

const (
	IPKindOther IPKind = iota
	IPKindPublic
	IPKindPrivate
	IPKindCGNAT
	IPKindLoopback
	IPKindIPv6ULA
	IPKindLinkLocal
	IPKindUnspecified
)

func (k IPKind) String() string {
	switch k {
	case IPKindPublic:
		return "public"
	case IPKindPrivate:
		return "private"
	case IPKindCGNAT:
		return "cgnat"
	case IPKindLoopback:
		return "loopback"
	case IPKindIPv6ULA:
		return "ipv6-ula"
	case IPKindLinkLocal:
		return "link-local"
	case IPKindUnspecified:
		return "unspecified"
	default:
		return "other"
	}
}

var (
	cgnatNet = func() *net.IPNet {
		_, n, _ := net.ParseCIDR("100.64.0.0/10")
		return n
	}()
	ulaNet = func() *net.IPNet {
		_, n, _ := net.ParseCIDR("fc00::/7")
		return n
	}()
)

// ClassifyIP classifies an IP address into IPKind.
func ClassifyIP(ip net.IP) IPKind {
	if ip == nil {
		return IPKindOther
	}

	// Check IPv4 (either IPv4 or IPv4-mapped IPv6)
	if ip4 := ip.To4(); ip4 != nil {
		if ip4.IsUnspecified() {
			return IPKindUnspecified
		}
		if ip4.IsLoopback() {
			return IPKindLoopback
		}
		if ip4.IsLinkLocalUnicast() || ip4.IsLinkLocalMulticast() {
			return IPKindLinkLocal
		}
		if cgnatNet.Contains(ip4) {
			return IPKindCGNAT
		}
		if ip4.IsPrivate() {
			return IPKindPrivate
		}
		if ip4.IsMulticast() || ip4.Equal(net.IPv4bcast) {
			return IPKindOther
		}
		return IPKindPublic
	}

	// IPv6
	if ip.IsUnspecified() {
		return IPKindUnspecified
	}
	if ip.IsLoopback() {
		return IPKindLoopback
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return IPKindLinkLocal
	}
	if ulaNet.Contains(ip) {
		return IPKindIPv6ULA
	}
	if ip.IsPrivate() {
		return IPKindPrivate
	}
	if ip.IsMulticast() {
		return IPKindOther
	}
	return IPKindPublic
}

// IsPublicIP returns true if ip is a public, routable IP address.
func IsPublicIP(ip net.IP) bool {
	return ClassifyIP(ip) == IPKindPublic
}

// ListenerPublicIPs finds public routable IP addresses associated with listenAddr.
// If listenAddr specifies 0.0.0.0, [::], or an unspecified host, it inspects
// interface addresses using ifaceAddrs.
func ListenerPublicIPs(listenAddr string, ifaceAddrs func() ([]net.Addr, error)) ([]net.IP, error) {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		// Could be host without port
		host = listenAddr
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
	}

	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		if ifaceAddrs == nil {
			ifaceAddrs = net.InterfaceAddrs
		}
		addrs, err := ifaceAddrs()
		if err != nil {
			return nil, fmt.Errorf("listing interface addresses: %w", err)
		}
		var publicIPs []net.IP
		seen := make(map[string]bool)
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && IsPublicIP(ip) {
				ipStr := ip.String()
				if !seen[ipStr] {
					seen[ipStr] = true
					publicIPs = append(publicIPs, ip)
				}
			}
		}
		return publicIPs, nil
	}

	// Specific host/IP
	ip := net.ParseIP(host)
	if ip != nil {
		if IsPublicIP(ip) {
			return []net.IP{ip}, nil
		}
		return nil, nil
	}

	// Hostname
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	var publicIPs []net.IP
	seen := make(map[string]bool)
	for _, ip := range ips {
		if IsPublicIP(ip) {
			ipStr := ip.String()
			if !seen[ipStr] {
				seen[ipStr] = true
				publicIPs = append(publicIPs, ip)
			}
		}
	}
	return publicIPs, nil
}
