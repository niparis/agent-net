package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// tailscaleCLI runs the tailscale command line, never through a shell. It
// looks on PATH first, then in the fallback list (the macOS app's binary).
type tailscaleCLI struct {
	search   string // a PATH-style list; empty uses $PATH
	fallback []string
}

var errNoTailscale = errors.New("tailscale is not installed")

func (c tailscaleCLI) path() (string, error) {
	if c.search == "" {
		if p, err := exec.LookPath("tailscale"); err == nil {
			return p, nil
		}
	} else {
		for _, dir := range filepath.SplitList(c.search) {
			if p := filepath.Join(dir, "tailscale"); isExecutableFile(p) {
				return p, nil
			}
		}
	}
	for _, p := range c.fallback {
		if isExecutableFile(p) {
			return p, nil
		}
	}
	return "", errNoTailscale
}

func isExecutableFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

func (c tailscaleCLI) run(ctx context.Context, args ...string) (string, error) {
	path, err := c.path()
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("tailscale %s: %s", args[0], firstLine(msg))
		}
		return "", fmt.Errorf("tailscale %s: %v", args[0], err)
	}
	return stdout.String(), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// tailscaleStatus is the part of `tailscale status --json` the daemon reads.
type tailscaleStatus struct {
	BackendState string
	Self         *tailscalePeer
	Peer         map[string]*tailscalePeer
}

type tailscalePeer struct {
	TailscaleIPs []string
	Online       bool
}

// peerOnline tells whether ip belongs to an online peer of this machine's
// tailnet. A peer in the list proves that both machines are on one tailnet.
func (c tailscaleCLI) peerOnline(ctx context.Context, ip string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, fileCommandTimeout)
	defer cancel()
	out, err := c.run(ctx, "status", "--json")
	if err != nil {
		return false, err
	}
	var st tailscaleStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		return false, fmt.Errorf("tailscale status: %v", err)
	}
	if st.BackendState != "" && st.BackendState != "Running" {
		return false, fmt.Errorf("tailscale is %s", st.BackendState)
	}
	for _, p := range st.Peer {
		if p == nil {
			continue
		}
		for _, a := range p.TailscaleIPs {
			if a == ip {
				return p.Online, nil
			}
		}
	}
	return false, nil
}

// send pushes one file to ip over Taildrop. The file travels under its own
// base name, which the caller has made the unique name.
func (c tailscaleCLI) send(ctx context.Context, file, ip string) error {
	ctx, cancel := context.WithTimeout(ctx, fileTaildropTimeout)
	defer cancel()
	_, err := c.run(ctx, "file", "cp", file, ip+":")
	return err
}

// get moves the files waiting in tailscale's inbox into dir. Linux only: the
// Mac app saves them straight into Downloads.
func (c tailscaleCLI) get(ctx context.Context, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, fileCommandTimeout)
	defer cancel()
	_, err := c.run(ctx, "file", "get", dir)
	return err
}

// tailnetRange is Tailscale's address range, 100.64.0.0/10.
var tailnetRange = netip.MustParsePrefix("100.64.0.0/10")

// localTailscaleIP returns this machine's Tailscale address from its network
// interfaces, or "". It runs no command, so hello stays fast.
func localTailscaleIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ip, ok := netip.AddrFromSlice(ipn.IP.To4()); ok && tailnetRange.Contains(ip) {
			return ip.String()
		}
	}
	return ""
}
