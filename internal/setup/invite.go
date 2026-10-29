package setup

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/a2a-ocs/internal/gateway"
)

// InviteLine is the one-line installer for a host.
func InviteLine(host, port, code, pin string) string {
	return fmt.Sprintf("curl -fsSL -k --pinnedpubkey '%s' 'https://%s/install/%s' | sh",
		pin, net.JoinHostPort(host, port), code)
}

// DefaultRouteIP returns the IPv4 address of the interface that holds the
// default route. Dialing UDP sends nothing; it only asks the kernel which
// local address it would use.
func DefaultRouteIP() (string, error) {
	c, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return "", err
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String(), nil
}

func isTailscale(ip string) bool {
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	p := net.ParseIP(ip)
	return p != nil && cgnat.Contains(p)
}

// TailscaleIP returns this machine's Tailscale address (100.64.0.0/10), or "".
func TailscaleIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && isTailscale(ipn.IP.String()) {
			return ipn.IP.String()
		}
	}
	return ""
}

// InviteOptions configure Invite.
type InviteOptions struct {
	Host string
	Port string
	Data string
}

// Invite creates a one-time code and returns the install lines: a label and
// the curl line for each address the gateway can be reached on.
func Invite(o InviteOptions) (lines [][2]string, err error) {
	// The CA exists once the gateway has started; the served script carries
	// its fingerprint.
	if _, err := gateway.CAFingerprint(o.Data); err != nil {
		return nil, err
	}
	pin, err := gateway.ServerPin(o.Data)
	if err != nil {
		return nil, err
	}
	st, err := gateway.OpenStore(filepath.Join(o.Data, "gateway.db"))
	if err != nil {
		return nil, err
	}
	defer st.Close()
	code, err := st.CreateEnrollCode(10 * time.Minute)
	if err != nil {
		return nil, err
	}
	host := o.Host
	if host == "" {
		if host, err = DefaultRouteIP(); err != nil {
			if host, err = os.Hostname(); err != nil {
				return nil, fmt.Errorf("cannot find this machine's address, pass --host: %w", err)
			}
		}
		label := "LAN"
		if isTailscale(host) {
			label = "Tailscale"
		}
		lines = append(lines, [2]string{label, InviteLine(host, o.Port, code, pin)})
		if ts := TailscaleIP(); ts != "" && ts != host {
			lines = append(lines, [2]string{"Tailscale", InviteLine(ts, o.Port, code, pin)})
		}
		return lines, nil
	}
	return [][2]string{{"", InviteLine(host, o.Port, code, pin)}}, nil
}
