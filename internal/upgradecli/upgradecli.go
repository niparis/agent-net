// Package upgradecli is `agent-net upgrade`: it upgrades this machine, and on
// the gateway host also tells every connected node to upgrade.
package upgradecli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/setup"
	"github.com/niparis/agent-net/internal/upgrade"
)

// Options configure Run.
type Options struct {
	Version  string // release to install on the gateway host; empty means the latest
	Force    bool
	Check    bool
	Rollback bool
	Timeout  time.Duration // how long to wait for the nodes (default 2 min)

	Current     string // the version of the running binary
	Exe         string // default: the running executable
	GatewayData string // gateway data dir; ca.pem there means this is the gateway host
	Base        string // release base URL; default: upgrade.ReleaseBase()
	Out         io.Writer
}

// sessionsNotes tells what a running agent session needs after an upgrade.
var sessionsNotes = []string{
	"sessions            Claude Code (started on v0.3.2 or later): switches to the new version by itself",
	"                    Pi: run /reload in the session",
	"                    OpenCode: reloads the plugin file by itself",
}

type run struct {
	Options
	noService bool
}

// Run upgrades, checks or rolls back this machine.
func Run(ctx context.Context, o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.Base == "" {
		o.Base = upgrade.ReleaseBase()
	}
	if o.Exe == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		o.Exe = exe
	}
	r := &run{Options: o, noService: os.Getenv("AGENT_NET_NO_SERVICE") == "1"}
	if o.Rollback && (o.Check || o.Version != "") {
		return errors.New("--rollback cannot be combined with --check or --version")
	}
	if _, err := os.Stat(filepath.Join(o.GatewayData, "ca.pem")); err == nil {
		return r.gatewayHost(ctx)
	}
	if _, _, err := daemon.LoadNode(daemon.ConfigDir()); err == nil {
		return r.joinedMachine(ctx)
	}
	return errors.New("agent-net is not set up on this machine (no gateway data in " + o.GatewayData +
		" and no node.json in " + daemon.ConfigDir() + ")")
}

func (r *run) say(format string, a ...any) { fmt.Fprintf(r.Out, format+"\n", a...) }

func (r *run) saySessions() {
	for _, l := range sessionsNotes {
		r.say("%s", l)
	}
}

func (r *run) devGuard() error {
	if r.Current == "dev" && !r.Force {
		return errors.New(`this is a dev build (version "dev"): upgrade would replace it with a release. Use --force to do it anyway`)
	}
	return nil
}

// ---- running setup and restarting ----

// runSetup runs `<exe> setup <args>`: the new binary restarts the services and
// refreshes the integrations.
func (r *run) runSetup(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, r.Exe, append([]string{"setup"}, args...)...)
	cmd.Stdout, cmd.Stderr = r.Out, r.Out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s setup failed: %w", r.Exe, err)
	}
	return nil
}

// restartDaemonWithoutService asks the running daemon to restart itself when
// no service manager does it (AGENT_NET_NO_SERVICE=1).
func (r *run) restartDaemonWithoutService(ctx context.Context) {
	if !r.noService {
		return
	}
	cl, err := client.Dial(daemon.SocketPath())
	if err != nil {
		r.say("daemon              not running, nothing to restart")
		return
	}
	defer cl.Close()
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := cl.Call(cctx, "hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol}); err == nil {
		_, err = cl.Call(cctx, "restart", nil)
	}
	if err != nil {
		r.say("daemon              restart failed (%v)", err)
		return
	}
	r.say("daemon              restarting")
}

// ---- joined machine ----

func (r *run) joinedMachine(ctx context.Context) error {
	nc, ca, err := daemon.LoadNode(daemon.ConfigDir())
	if err != nil {
		return err
	}
	hc, err := daemon.PinnedClient(ca)
	if err != nil {
		return err
	}
	hc.Timeout = 5 * time.Minute
	gwVer, gwErr := upgrade.GatewayVersion(ctx, hc, nc.GatewayURL)
	latest, latestErr := upgrade.LatestTag(ctx, r.Base)

	switch {
	case r.Check:
		r.say("this machine   %s (%s)", r.Current, r.Exe)
		r.say("latest release %s", orErr(latest, latestErr))
		r.sayHold()
		r.say("gateway        %s", orErr(gwVer, gwErr))
		r.printNodesViaDaemon(ctx)
		return nil
	case r.Rollback:
		return r.rollback(ctx, false)
	}
	if err := r.devGuard(); err != nil {
		return err
	}
	if r.Version != "" {
		return errors.New("--version works on the gateway host only; a joined machine installs what its gateway serves")
	}
	if gwErr != nil {
		return fmt.Errorf("cannot reach the gateway %s: %w", nc.GatewayURL, gwErr)
	}
	res, err := upgrade.FromGateway(ctx, hc, nc.GatewayURL, r.Exe, r.Current, "")
	if err != nil {
		return fmt.Errorf("nothing changed: %w", err)
	}
	r.clearHold()
	if res.To == r.Current && !res.Replaced && !r.Force {
		r.say("already on %s (the gateway serves %s)", r.Current, res.To)
		r.hintGatewayBehind(gwVer, latest, latestErr)
		return nil
	}
	r.say("agent-net           %s -> %s (previous binary kept as %s)", r.Current, res.To, upgrade.PrevPath(r.Exe))
	r.hintGatewayBehind(gwVer, latest, latestErr)
	if err := r.runSetup(ctx); err != nil {
		return err
	}
	r.restartDaemonWithoutService(ctx)
	r.saySessions()
	return nil
}

func (r *run) hintGatewayBehind(gwVer, latest string, latestErr error) {
	if latestErr == nil && gwVer != "" && gwVer != latest && versionLess(gwVer, latest) {
		r.say("hint: the gateway runs %s but the latest release is %s. Run agent-net upgrade on the gateway first, then here.", gwVer, latest)
	}
}

func (r *run) printNodesViaDaemon(ctx context.Context) {
	cl, err := client.Dial(daemon.SocketPath())
	if err != nil {
		r.say("nodes          unknown (the daemon is not running)")
		return
	}
	defer cl.Close()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := cl.Call(cctx, "hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol}); err != nil {
		r.say("nodes          unknown (%v)", err)
		return
	}
	raw, err := cl.Call(cctx, "nodes", nil)
	if err != nil {
		r.say("nodes          unknown (%v)", err)
		return
	}
	var info protocol.NodesInfo
	if json.Unmarshal(raw, &info) != nil {
		return
	}
	r.printNodes(info.Nodes)
}

func (r *run) printNodes(nodes []protocol.NodeView) {
	r.say("")
	tw := tabwriter.NewWriter(r.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "HOST\tNODE\tVERSION\tONLINE")
	for _, n := range nodes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\n", n.Hostname, n.NodeID, showVersion(n.Version), n.Online)
	}
	tw.Flush()
}

// ---- rollback hold ----

// holdAt makes this machine's daemon skip version v (and older) when it
// upgrades itself, so a rollback is not undone at the next reconnect.
func (r *run) holdAt(v string) {
	if _, ok := upgrade.ParseRelease(v); !ok {
		return
	}
	if err := upgrade.SetHold(daemon.StateDir(), v); err != nil {
		r.say("auto-upgrade        cannot hold %s: %v", v, err)
		return
	}
	r.say("auto-upgrade        held at %s (a newer release, or agent-net upgrade, lifts it)", v)
}

// clearHold is for a manual upgrade: the person chose to move on.
func (r *run) clearHold() {
	if err := upgrade.ClearHold(daemon.StateDir()); err != nil {
		r.say("auto-upgrade        cannot clear the rollback hold: %v", err)
	}
}

func (r *run) sayHold() {
	if note := upgrade.LoadAuto(daemon.StateDir()).HoldNote(); note != "" {
		r.say("auto-upgrade   %s", strings.TrimPrefix(note, "auto-upgrade "))
	}
}

// ---- rollback ----

func (r *run) rollback(ctx context.Context, gatewayHost bool) error {
	before := r.Current
	now, err := upgrade.Rollback(ctx, r.Exe)
	if err != nil {
		return err
	}
	r.say("agent-net           %s -> %s (the version you left is kept as %s)", before, now, upgrade.PrevPath(r.Exe))
	r.holdAt(before)
	var args []string
	if gatewayHost {
		args = r.gatewaySetupArgs(nil)
	}
	if err := r.runSetup(ctx, args...); err != nil {
		return err
	}
	if gatewayHost {
		r.restartGatewayWithoutService(ctx)
	}
	r.restartDaemonWithoutService(ctx)
	r.saySessions()
	return nil
}

// ---- gateway host ----

func (r *run) admin() (*adminClient, error) {
	raw, err := os.ReadFile(gateway.AdminFile(r.GatewayData))
	if err != nil {
		return nil, errors.New("the gateway is not running (no " + gateway.AdminFile(r.GatewayData) + ")")
	}
	var info gateway.AdminInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(filepath.Join(r.GatewayData, "ca.pem"))
	if err != nil {
		return nil, err
	}
	hc, err := daemon.PinnedClient(ca)
	if err != nil {
		return nil, err
	}
	return &adminClient{info: info, hc: hc}, nil
}

type adminClient struct {
	info gateway.AdminInfo
	hc   *http.Client
}

func (a *adminClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.info.URL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.info.Token)
	resp, err := a.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("gateway answered %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (a *adminClient) nodes(ctx context.Context) (protocol.NodesInfo, error) {
	var info protocol.NodesInfo
	return info, a.do(ctx, "GET", "/v1/admin/nodes", nil, &info)
}

// gatewaySetupArgs are the flags for `setup --gateway`, taken from the running
// gateway when it is known.
func (r *run) gatewaySetupArgs(a *adminClient) []string {
	args := []string{"--gateway", "--data", r.GatewayData}
	dist, port := setup.DefaultDist(), setup.GatewayPort
	if a == nil {
		a, _ = r.admin()
	}
	if a != nil {
		if a.info.Dist != "" {
			dist = a.info.Dist
		}
		if u, err := url.Parse(a.info.URL); err == nil && u.Port() != "" {
			port = u.Port()
		}
	}
	return append(args, "--dist", dist, "--port", port)
}

func (r *run) restartGatewayWithoutService(ctx context.Context) {
	if !r.noService {
		return
	}
	a, err := r.admin()
	if err != nil {
		r.say("gateway             not running, nothing to restart")
		return
	}
	if err := a.do(ctx, "POST", "/v1/admin/restart", nil, nil); err != nil {
		r.say("gateway             restart failed (%v)", err)
		return
	}
	r.say("gateway             restarting")
}

func (r *run) gatewayHost(ctx context.Context) error {
	if r.Rollback {
		return r.rollback(ctx, true)
	}
	latest, latestErr := upgrade.LatestTag(ctx, r.Base)
	if r.Check {
		return r.gatewayCheck(ctx, latest, latestErr)
	}
	if err := r.devGuard(); err != nil {
		return err
	}
	tag := r.Version
	if tag == "" {
		if latestErr != nil {
			return latestErr
		}
		tag = latest
	}

	// Remember who is online, to wait for them after the gateway restarts.
	var wasOnline []string
	before := map[string]string{}
	if a, err := r.admin(); err == nil {
		if info, err := a.nodes(ctx); err == nil {
			for _, n := range info.Nodes {
				before[n.NodeID] = n.Version
				if n.Online {
					wasOnline = append(wasOnline, n.NodeID)
				}
			}
		}
	}

	if tag == r.Current && !r.Force {
		r.say("already on %s", tag)
	} else if err := r.installRelease(ctx, tag); err != nil {
		return err
	}
	r.clearHold()
	return r.upgradeNodes(ctx, tag, wasOnline, before)
}

// installRelease is steps 2 and 3: download and verify everything, then
// replace the binary, refresh the dist dir and run setup.
func (r *run) installRelease(ctx context.Context, tag string) error {
	tmp, err := os.MkdirTemp("", "agent-net-upgrade.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	r.say("downloading %s", tag)
	hc := &http.Client{Timeout: 10 * time.Minute}
	if err := upgrade.Download(ctx, hc, upgrade.TagURL(r.Base, tag), upgrade.AllAssets(), tmp); err != nil {
		return fmt.Errorf("nothing changed: %w", err)
	}
	mine := filepath.Join(tmp, upgrade.ThisAsset())
	got, err := upgrade.BinaryVersion(ctx, mine)
	if err != nil {
		return fmt.Errorf("nothing changed: %w", err)
	}
	if got != tag {
		return fmt.Errorf("nothing changed: the %s binary in release %s reports version %s", upgrade.ThisAsset(), tag, got)
	}
	a, _ := r.admin()
	args := r.gatewaySetupArgs(a)
	dist := args[indexOf(args, "--dist")+1]

	if err := upgrade.Replace(r.Exe, mine); err != nil {
		return fmt.Errorf("nothing changed: %w", err)
	}
	for _, name := range upgrade.AllAssets() {
		if err := upgrade.PlaceFile(filepath.Join(tmp, name), filepath.Join(dist, name)); err != nil {
			return fmt.Errorf("the binary is replaced but the dist dir is not refreshed: %w", err)
		}
	}
	r.say("agent-net           %s -> %s (previous binary kept as %s)", r.Current, tag, upgrade.PrevPath(r.Exe))
	if err := r.runSetup(ctx, args...); err != nil {
		return err
	}
	r.restartGatewayWithoutService(ctx)
	r.restartDaemonWithoutService(ctx)
	return nil
}

func indexOf(s []string, v string) int {
	for i := range s {
		if s[i] == v {
			return i
		}
	}
	return -1
}

// upgradeNodes is step 4: wait for the gateway (and the nodes that were
// online) to be back, tell every connected node to upgrade, print the result.
func (r *run) upgradeNodes(ctx context.Context, tag string, wasOnline []string, before map[string]string) error {
	a, err := r.waitGateway(ctx, tag, 45*time.Second)
	if err != nil {
		r.say("nodes               not upgraded: %v", err)
		return err
	}
	a.hc.Timeout = r.Timeout + 30*time.Second
	var out struct {
		Results []protocol.UpgradeResult `json:"results"`
	}
	// The gateway waits for the nodes that were online to reconnect (they are
	// not offline, only reconnecting after the restart) before it asks them.
	req := map[string]any{"version": tag, "timeout_seconds": int(r.Timeout / time.Second), "expect_online": wasOnline}
	if err := a.do(ctx, "POST", "/v1/admin/upgrade", req, &out); err != nil {
		return fmt.Errorf("cannot ask the nodes to upgrade: %w", err)
	}
	failed := 0
	for _, res := range out.Results {
		// A node that was on an older version before the gateway restarted, and
		// is already on the target now, caught up by itself.
		if v, ok := before[res.NodeID]; ok {
			if res.Status == protocol.UpgradeOK && v != tag && res.From == tag {
				res.Detail = "upgraded on reconnect"
			}
			// A node on this host may already have restarted; show where it started.
			res.From = v
		}
		r.say("%s", FormatResult(res))
		if res.Status == protocol.UpgradeFailed {
			failed++
		}
	}
	r.saySessions()
	if failed > 0 {
		return fmt.Errorf("%d node(s) failed to upgrade", failed)
	}
	return nil
}

// FormatResult renders one per-node line.
func FormatResult(res protocol.UpgradeResult) string {
	var status string
	switch res.Status {
	case protocol.UpgradeOK:
		status = "ok"
		if res.Detail != "" {
			status += " (" + res.Detail + ")"
		}
	case protocol.UpgradeOffline:
		status = "offline (" + res.Detail + ")"
	default:
		status = "failed (" + res.Detail + ")"
	}
	return fmt.Sprintf("%s %s  %s -> %s  %s", res.Hostname, res.NodeID, showVersion(res.From), res.To, status)
}

func showVersion(v string) string {
	if v == "" || v == "step2" {
		return "unknown"
	}
	return v
}

// waitGateway waits until the gateway answers and runs the wanted version.
func (r *run) waitGateway(ctx context.Context, tag string, d time.Duration) (*adminClient, error) {
	deadline := time.Now().Add(d)
	last := errors.New("the gateway is not running")
	for {
		a, err := r.admin()
		if err == nil {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			var info protocol.NodesInfo
			info, err = a.nodes(cctx)
			cancel()
			if err == nil {
				if info.GatewayVersion == tag {
					return a, nil
				}
				err = fmt.Errorf("the gateway still runs %s, not %s (restart it, then run agent-net upgrade again)", showVersion(info.GatewayVersion), tag)
			}
		}
		last = err
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, last
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (r *run) gatewayCheck(ctx context.Context, latest string, latestErr error) error {
	r.say("this machine   %s (%s)", r.Current, r.Exe)
	r.say("latest release %s", orErr(latest, latestErr))
	r.sayHold()
	a, err := r.admin()
	var info protocol.NodesInfo
	if err == nil {
		info, err = a.nodes(ctx)
	}
	if err != nil {
		r.say("gateway        not reachable (%v)", err)
		return nil
	}
	r.say("gateway        %s", showVersion(info.GatewayVersion))
	r.printNodes(info.Nodes)
	return nil
}

func orErr(v string, err error) string {
	if err != nil {
		return "unavailable (" + err.Error() + ")"
	}
	return v
}

// versionLess reports whether a is an older release than b. Tags that do not
// look like vX.Y.Z count as different, so the hint is shown.
func versionLess(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return a != b
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
