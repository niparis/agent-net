// Package funnel controls Tailscale Funnel for the connected-apps listener.
// Callers should read Status and ChoosePort before enabling a new listener.
package funnel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var (
	ErrNotInstalled     = errors.New("Tailscale is not installed on this machine. Use your own tunnel and paste its URL.")
	ErrNotRunning       = errors.New("Tailscale is not running. Start it, then try again.")
	ErrNeedsOperator    = errors.New("Run once in a terminal: sudo tailscale set --operator=$USER, then try again.")
	ErrFunnelNotAllowed = errors.New("Tailscale Funnel is not allowed for this node.")
)

const funnelHelp = "https://tailscale.com/s/no-funnel"

// FunnelNotAllowedError carries the approval or help link supplied by Tailscale.
// When status has no capability, Link is the upstream Funnel help redirect.
// Use errors.Is(err, ErrFunnelNotAllowed) or errors.As to retrieve Link.
type FunnelNotAllowedError struct{ Link string }

func (e *FunnelNotAllowedError) Error() string {
	return "Allow Funnel for this tailnet: open " + e.Link + ", then try again."
}
func (e *FunnelNotAllowedError) Unwrap() error { return ErrFunnelNotAllowed }

// Runner executes tailscale with arguments, without invoking a shell.
type Runner interface {
	Run(ctx context.Context, args ...string) (stdout, stderr string, err error)
}

// ExecRunner uses tailscale on PATH, falling back to the macOS app bundle.
// Its zero value is ready to use. Tests should supply a fake Runner instead.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, args ...string) (string, string, error) {
	path, err := exec.LookPath("tailscale")
	if err != nil && runtime.GOOS == "darwin" {
		const app = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
		if fi, statErr := os.Stat(app); statErr == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
			path, err = app, nil
		}
	}
	if err != nil {
		return "", "", err
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return stdout.String(), stderr.String(), err
}

// Info is a status snapshot. Ports includes both Serve and Funnel, including
// foreground entries. Values describe the configuration; key presence reserves
// a port even when its description is empty. Do not use stale snapshots to
// select ports after another process has changed the Tailscale configuration.
type Info struct {
	Installed     bool
	Running       bool
	DNSName       string
	FunnelAllowed bool
	Ports         map[int]string
}

// Status reads the local node and the shared Serve/Funnel configuration.
// It returns partial Info alongside actionable errors. A failed or malformed
// config read is an error, never an empty (apparently available) port list.
func Status(ctx context.Context, r Runner) (Info, error) {
	info := Info{Ports: make(map[int]string)}
	stdout, stderr, err := r.Run(ctx, "status", "--json")
	info.Installed = !errors.Is(err, exec.ErrNotFound) && !errors.Is(err, os.ErrNotExist)
	if err != nil {
		return info, commandError(stdout, stderr, err)
	}
	var st struct {
		BackendState string
		Self         *struct {
			DNSName      string
			Capabilities []string
			CapMap       map[string]json.RawMessage
		}
	}
	if err := json.Unmarshal([]byte(stdout), &st); err != nil {
		return info, fmt.Errorf("decode tailscale status: %w", err)
	}
	info.Running = st.BackendState == "Running"
	if !info.Running {
		return info, ErrNotRunning
	}
	if st.Self == nil || st.Self.DNSName == "" {
		return info, errors.New("tailscale status has no local DNS name")
	}
	info.DNSName = strings.TrimSuffix(st.Self.DNSName, ".")
	for _, cap := range st.Self.Capabilities {
		if funnelCap(cap) {
			info.FunnelAllowed = true
		}
	}
	for cap := range st.Self.CapMap {
		if funnelCap(cap) {
			info.FunnelAllowed = true
		}
	}
	// Both commands return the same ipn.ServeConfig. Serve status also works
	// before Funnel is enabled and includes existing private Serve listeners.
	stdout, stderr, err = r.Run(ctx, "serve", "status", "--json")
	if err != nil {
		return info, commandError(stdout, stderr, err)
	}
	var cfg serveConfig
	if err := json.Unmarshal([]byte(stdout), &cfg); err != nil {
		return info, fmt.Errorf("decode tailscale serve status: %w", err)
	}
	if err := cfg.ports(info.Ports); err != nil {
		return info, fmt.Errorf("decode tailscale serve ports: %w", err)
	}
	if !info.FunnelAllowed {
		return info, &FunnelNotAllowedError{Link: funnelHelp}
	}
	return info, nil
}
func funnelCap(cap string) bool { return cap == "funnel" || cap == "https://tailscale.com/cap/funnel" }

type serveConfig struct {
	TCP         map[string]json.RawMessage
	Web         map[string]json.RawMessage
	AllowFunnel map[string]bool
	Foreground  map[string]*serveConfig
}

func (c *serveConfig) ports(ports map[int]string) error {
	for key, value := range c.TCP {
		port, err := parsePort(key)
		if err != nil {
			return err
		}
		ports[port] = "TCP " + string(value)
	}
	// Sort so summaries are stable even with several hostnames on one port.
	keys := make([]string, 0, len(c.Web))
	for key := range c.Web {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, p, err := net.SplitHostPort(key)
		if err != nil {
			return err
		}
		port, err := parsePort(p)
		if err != nil {
			return err
		}
		ports[port] += " Web " + key + " " + string(c.Web[key])
	}
	for key, enabled := range c.AllowFunnel {
		if !enabled {
			continue
		}
		_, p, err := net.SplitHostPort(key)
		if err != nil {
			return err
		}
		port, err := parsePort(p)
		if err != nil {
			return err
		}
		if _, exists := ports[port]; !exists {
			ports[port] = "Funnel"
		}
	}
	for _, cfg := range c.Foreground {
		if cfg != nil {
			if err := cfg.ports(ports); err != nil {
				return err
			}
		}
	}
	return nil
}
func parsePort(s string) (int, error) {
	port, err := strconv.Atoi(s)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return port, nil
}

// ChoosePort selects the first unused Funnel port. It never reuses a port
// with an existing Serve or Funnel entry, even if it targets our listener.
func ChoosePort(info Info) (int, error) {
	for _, port := range []int{443, 8443, 10000} {
		if _, used := info.Ports[port]; !used {
			return port, nil
		}
	}
	return 0, errors.New("Tailscale ports 443, 8443 and 10000 are already in use. Use your own tunnel and paste its URL.")
}

// On enables a background HTTPS Funnel on a previously selected free port.
// The caller owns checking the port and retaining it for Off. This operation
// does not atomically reserve the port against other Tailscale CLI users.
func On(ctx context.Context, r Runner, port int, target string) error {
	stdout, stderr, err := r.Run(ctx, "funnel", "--bg", "--https="+strconv.Itoa(port), target)
	return commandError(stdout, stderr, err)
}

// Off removes the selected listener. A missing handler/config is already off.
// Only pass a port owned by this caller, never one serving another application.
func Off(ctx context.Context, r Runner, port int) error {
	stdout, stderr, err := r.Run(ctx, "funnel", "--https="+strconv.Itoa(port), "off")
	mapped := commandError(stdout, stderr, err)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && mapped != nil &&
		(strings.HasPrefix(strings.TrimSpace(stderr), "error: handler does not exist") || strings.TrimSpace(stderr) == "serve config does not exist") {
		return nil
	}
	return mapped
}

// URL returns the public origin, omitting HTTPS's default port.
func URL(info Info, port int) string {
	host := strings.TrimSuffix(info.DNSName, ".")
	if port != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return "https://" + host
}

var approvalURL = regexp.MustCompile(`https://login\.tailscale\.com/[^\s<>"']+`)

func commandError(stdout, stderr string, err error) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return ErrNotInstalled
	}
	output := stderr + "\n" + stdout
	// The feature-enablement flow can print its link to stdout and exit 0.
	if link := approvalURL.FindString(output); link != "" && strings.Contains(strings.ToLower(output), "funnel") {
		return &FunnelNotAllowedError{Link: strings.TrimRight(link, ".,)")}
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if strings.Contains(stderr, "Access denied") && strings.Contains(stderr, "--operator") {
		return ErrNeedsOperator
	}
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "failed to connect to local tailscaled") || strings.Contains(lower, "tailscale is stopped") || strings.Contains(lower, "tailscale is not running") {
		return ErrNotRunning
	}
	if strings.Contains(lower, "funnel not available") {
		link := funnelHelp
		if strings.Contains(output, "https://tailscale.com/s/https") {
			link = "https://tailscale.com/s/https"
		}
		return &FunnelNotAllowedError{Link: link}
	}
	if detail := strings.TrimSpace(stderr); detail != "" {
		return fmt.Errorf("tailscale: %s: %w", detail, err)
	}
	return fmt.Errorf("tailscale: %w", err)
}
