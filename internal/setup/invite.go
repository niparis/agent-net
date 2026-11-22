package setup

import "github.com/niparis/agent-net/internal/gateway"

// InviteOptions configure Invite.
type InviteOptions = gateway.InviteOptions

// InviteLine is the one-line installer for a host.
func InviteLine(host, port, code, pin string) string {
	return gateway.InviteLine(host, port, code, pin)
}

// TailscaleIP returns this machine's Tailscale address, or "".
func TailscaleIP() string { return gateway.TailscaleIP() }

// Invite creates a one-time code and returns the install lines: a label and
// the curl line for each address the gateway can be reached on.
func Invite(o InviteOptions) ([][2]string, error) { return gateway.Invite(o) }
