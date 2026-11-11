package setup

import (
	"bufio"
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/funnel"
	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/tsnode"
)

// GatewayPort is the port the gateway service listens on.
const GatewayPort = "7420"

// Options configure Setup and Uninstall.
type Options struct {
	Join          string        // join string; empty means keep the current credentials
	Gateway       bool          // also install and start the gateway on this machine
	NoService     bool          // do not write or start services
	NoAutoUpgrade bool          // save automatic upgrade opt-out in node.json
	AutoUpgrade   bool          // clear the saved opt-out
	NoLaunch      bool          // refuse workers on this machine (saved in node.json)
	Launch        bool          // accept workers again after --no-launch
	Purge         bool          // uninstall only: also remove credentials and state
	Data          string        // gateway data dir
	FunnelRunner  funnel.Runner // optional Tailscale runner
	// TailscaleName gives connected apps agent-net's own Tailscale node with
	// this name (decision 0058). TailscaleAuthKeyFile is an optional path to
	// a file with an auth key. TailscaleMachine goes back to the machine's
	// node. Empty and false keep the saved choice.
	TailscaleName        string
	TailscaleAuthKeyFile string
	TailscaleMachine     bool
	TailscaleNode        tsnode.Opener // optional; tests use a fake node
	Dist                 string        // gateway dist dir
	Port                 string        // gateway port (default 7420)
	Exe                  string        // path of the agent-net binary; default: this binary
	All                  string        // "" ask; AllYes or AllNo answer every integration without a prompt
	PeerMessages         string        // "" leave to All/network; PeerMessagesAccept or PeerMessagesNo is a local answer
	CodexTools           string        // "" leave; CodexToolsApprove or CodexToolsNo is a local answer
	HintHook             string        // "" leave; HintHookYes or HintHookNo is a local answer
	PolicyHook           string        // "" leave; PolicyHookYes or PolicyHookNo is a local answer
	NeedsHook            string        // "" leave; NeedsHookYes or NeedsHookNo is a local answer
	WorkersMod           string        // "" leave; WorkersModYes or WorkersModNo is a local answer
	QuotaOnly            bool          // run only the quota setup step
	QuotaAXI             string        // "" leave; QuotaAXIYes or QuotaAXINo is a local answer
	Out                  io.Writer
}

// DefaultDist is where the gateway serves binaries from.
func DefaultDist() string {
	return filepath.Join(home(), ".local", "share", "agent-net", "dist")
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

type runner struct {
	Options
	failed         error
	failedStep     string
	colors         setupColor
	joining        bool
	foundHarnesses []string
	statusClient   *client.Client
	statusInfo     daemonInfo
	interactive    bool
	freshGateway   bool
	titleStore     ctoTitleStore
	// want is the answer for the seven integrations. When it is set, the steps
	// apply it and turn an item off as well as on. When it is nil, the steps
	// keep their old ask-once behavior.
	want *protocol.Integrations

	tailscaleIP      func() string
	gatewayListen    string
	installServiceFn func(s Service) (string, error)
}

func newRunner(o Options) (*runner, error) {
	interactive := o.Out == nil && setupHasTerminal()
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Exe == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		o.Exe = exe
	}
	if os.Getenv("AGENT_NET_NO_SERVICE") == "1" {
		o.NoService = true
	}
	return &runner{Options: o, colors: defaultSetupColor(), interactive: interactive}, nil
}

// setupHasTerminal checks stdin and the controlling terminal without sending
// terminal queries. Setup prompts only when a person can answer them.
func setupHasTerminal() bool {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return true
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	defer tty.Close()
	return term.IsTerminal(int(tty.Fd()))
}

// step prints one result line. A non-nil err prints as a failure.
func (r *runner) step(name, result string, err error) {
	if err != nil {
		result = "failed (" + err.Error() + ")"
		if r.failed == nil {
			r.failed = fmt.Errorf("%s: %w", name, err)
			r.failedStep = name
		}
	} else if harness := setupHarness(name); harness != "" && !strings.Contains(result, "not found") {
		r.foundHarnesses = appendUnique(r.foundHarnesses, harness)
	}
	line := fmt.Sprintf("%-21s %s", name, result)
	if err != nil {
		line = r.colors.failure(line)
	} else if strings.HasPrefix(result, "skipped") {
		line = fmt.Sprintf("%-21s %s", name, r.colors.faint(result))
	}
	fmt.Fprintln(r.Out, line)
}

func (r *runner) hint(format string, a ...any) {
	fmt.Fprintf(r.Out, "%-21s %s\n", "", r.colors.faint(fmt.Sprintf(format, a...)))
}

// Setup makes this machine part of the network, prints its setup steps, and
// finishes with a summary when the setup is not quota-only.
func Setup(ctx context.Context, o Options) error {
	if o.NoAutoUpgrade && o.AutoUpgrade {
		return errors.New("--no-auto-upgrade and --auto-upgrade cannot be used together")
	}
	r, err := newRunner(o)
	if err != nil {
		return err
	}
	if r.QuotaOnly {
		r.want = &protocol.Integrations{QuotaAXI: true}
		r.quotaAXI(ctx)
		return r.failed
	}
	defer r.finish(ctx)
	if r.Gateway {
		r.freshGateway = !gatewayDataExists(r.gatewayData())
		r.setupGateway(ctx)
		if r.failed != nil {
			return r.failed
		}
	}
	if err := r.join(ctx); err != nil {
		r.step("join", "", err)
		return r.failed
	}
	r.workersSetting()
	if r.NoAutoUpgrade || r.AutoUpgrade {
		if err := daemon.SetNoAutoUpgrade(daemon.ConfigDir(), r.NoAutoUpgrade); err != nil {
			r.step("auto-upgrade", "", err)
			return r.failed
		}
	}
	r.daemonService()
	r.chooseIntegrations(ctx)
	r.askCTOTitle(ctx)
	r.claudeMCP()
	r.claudeMouseSettings()
	r.claudeAutoUpdateSettings()
	r.claudePeerMessages()
	r.codexMCP()
	r.claudeHintHook()
	r.claudePolicyHook()
	r.claudeNeedsHook()
	r.claudeWorkersMod()
	r.quotaAXI(ctx)
	r.recordApplied()
	r.piExtension()
	r.opencodePlugin()
	r.guidance()
	r.daemonStatus(ctx)
	return r.failed
}

// ---- gateway ----

func (r *runner) getTailscaleIP() string {
	if r.tailscaleIP != nil {
		return r.tailscaleIP()
	}
	return TailscaleIP()
}

func (r *runner) askTailscaleListen(tsIP string) bool {
	if r.All == AllYes {
		return true
	}
	if r.All == AllNo {
		return false
	}
	if !r.interactive {
		return false
	}
	tty, err := openTTY()
	if err != nil {
		return false
	}
	defer tty.Close()
	fmt.Fprintf(r.Out, "Tailscale is available on this machine (%s).\n", tsIP)
	fmt.Fprintf(r.Out, "Listen on Tailscale address instead of all interfaces? [Y/n] ")
	rd := bufio.NewReader(tty)
	line, _ := rd.ReadString('\n')
	raw := strings.TrimSpace(strings.ToLower(line))
	return raw == "" || raw == "y" || raw == "yes"
}

func (r *runner) setupGateway(ctx context.Context) {
	data := r.Data
	dist := r.Dist
	if dist == "" {
		dist = DefaultDist()
	}
	listenHost := "0.0.0.0"
	if r.freshGateway && !r.joining {
		if ts := r.getTailscaleIP(); ts != "" {
			if r.askTailscaleListen(ts) {
				listenHost = ts
				r.gatewayListen = ts
			}
		}
	}
	// Before the gateway (re)starts, so the new gateway reads the choice.
	r.setTailscaleChoice()
	if r.NoService {
		r.step("gateway service", "skipped (no service)", nil)
	} else {
		args := []string{"gateway", "--listen", net.JoinHostPort(listenHost, r.port()), "--dist", dist}
		if data != "" {
			args = append(args, "--data", data)
		}
		res, err := r.installService(Service{Name: "gateway", Args: args})
		r.step("gateway service", res, err)
	}
	res, err := r.syncDist(dist)
	r.step("gateway binaries", res, err)
}

// setTailscaleChoice saves or removes the own-node choice in the gateway data
// folder. The gateway reads it at start; without a flag the saved choice stays.
func (r *runner) setTailscaleChoice() {
	data := r.gatewayData()
	switch {
	case r.TailscaleMachine:
		r.step("Tailscale name", "ok (the machine's own node)", tsnode.RemoveChoice(data))
	case r.TailscaleName != "":
		c := tsnode.Choice{Name: r.TailscaleName, AuthKeyFile: r.TailscaleAuthKeyFile}
		r.step("Tailscale name", "ok ("+c.Name+": connected apps use agent-net's own node)", tsnode.SaveChoice(data, c))
	}
}

// InvalidateSignedDist removes release metadata before a local binary changes.
// Older clients can still use gateway-generated checksums; signed clients fail
// closed until a complete signed release is installed again.
func InvalidateSignedDist(dist string) error {
	// Remove the signature first, so signed clients cannot trust stale sums.
	for _, name := range []string{"checksums.txt.sig", "checksums.txt"} {
		if err := os.Remove(filepath.Join(dist, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot remove stale checksum file %s: %w", name, err)
		}
	}
	return nil
}

// syncDist makes sure the dist dir has this binary for its own platform.
func (r *runner) syncDist(dist string) (string, error) {
	if strings.HasSuffix(r.Exe, ".test") {
		return "", fmt.Errorf("refusing to copy test binary %s to gateway dist", r.Exe)
	}
	// A test executable can have been renamed, so check its build info too.
	if info, err := buildinfo.ReadFile(r.Exe); err == nil && strings.HasSuffix(info.Path, ".test") {
		return "", fmt.Errorf("refusing to copy test binary %s to gateway dist", r.Exe)
	}
	target := filepath.Join(dist, fmt.Sprintf("agent-net-%s-%s", runtime.GOOS, runtime.GOARCH))
	src, err := os.ReadFile(r.Exe)
	if err != nil {
		return "", err
	}
	old, err := os.ReadFile(target)
	if err == nil && bytes.Equal(old, src) {
		return "ok (unchanged)", nil
	}
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return "", err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, src, 0o755); err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	if err := InvalidateSignedDist(dist); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		return "", err
	}
	if old == nil {
		return "ok", nil
	}
	return "updated", nil
}

// ---- join ----

func (r *runner) joinedWith(fp string) bool {
	nc, _, err := daemon.LoadNode(daemon.ConfigDir())
	return err == nil && nc.CASHA256 == fp
}

func (r *runner) join(ctx context.Context) error {
	joinStr := r.Join
	var fp string
	switch {
	case joinStr != "":
		_, _, f, err := daemon.ParseJoinString(joinStr)
		if err != nil {
			return err
		}
		fp = f
	case r.Gateway:
		// The gateway joins itself with a code made straight in its store.
		var err error
		if fp, err = r.waitForCA(); err != nil {
			return err
		}
	default:
		if _, _, err := daemon.LoadNode(daemon.ConfigDir()); err != nil {
			return errors.New("not joined yet: run `agent-net setup --join <join-string>` or use the invite line")
		}
		r.step("join", "skipped (already joined)", nil)
		return nil
	}
	if r.joinedWith(fp) {
		r.step("join", "skipped (already joined)", nil)
		return nil
	}
	r.joining = joinStr != ""
	if joinStr == "" {
		host := "localhost"
		if r.gatewayListen != "" {
			host = r.gatewayListen
		}
		if !r.NoService {
			if err := waitForPort(net.JoinHostPort(host, r.port()), 10*time.Second); err != nil {
				return err
			}
		}
		st, err := gateway.OpenStore(filepath.Join(r.gatewayData(), "gateway.db"))
		if err != nil {
			return err
		}
		code, err := st.CreateEnrollCode(time.Minute)
		st.Close()
		if err != nil {
			return err
		}
		joinStr = fmt.Sprintf("https://%s:%s#%s#%s", host, r.port(), code, fp)
	}
	_, _, perr := daemon.LoadNode(daemon.ConfigDir())
	nc, err := daemon.Join(ctx, joinStr, daemon.ConfigDir())
	if err != nil {
		return err
	}
	res := "ok (node " + nc.NodeID + ")"
	if perr == nil {
		res = "updated (node " + nc.NodeID + ", new gateway)"
	}
	r.step("join", res, nil)
	return nil
}

func (r *runner) port() string {
	if r.Port != "" {
		return r.Port
	}
	return GatewayPort
}

func (r *runner) gatewayData() string {
	if r.Data != "" {
		return r.Data
	}
	if d := os.Getenv("AGENT_NET_GATEWAY_DATA"); d != "" {
		return d
	}
	return filepath.Join(home(), ".local", "state", "agent-net", "gateway")
}

// gatewayDataExists says whether a gateway already ran with this data folder.
// A gateway that ran before is a rerun or an upgrade, not a fresh install.
func gatewayDataExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "ca.pem"))
	return err == nil
}

// waitForCA waits for the gateway service to create its CA.
func (r *runner) waitForCA() (string, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		fp, err := gateway.CAFingerprint(r.gatewayData())
		if err == nil {
			return fp, nil
		}
		if r.NoService || time.Now().After(deadline) {
			return "", err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitForPort(addr string, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("gateway is not listening on %s", addr)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// ---- workers ----

// workersSetting saves --no-launch or --launch in node.json and says what the
// machine does now. A setting that was saved earlier stays when neither flag is given.
func (r *runner) workersSetting() {
	if r.NoLaunch && r.Launch {
		r.step("workers", "", errors.New("use --no-launch or --launch, not both"))
		return
	}
	if r.NoLaunch || r.Launch {
		if err := daemon.SetNoLaunch(daemon.ConfigDir(), r.NoLaunch); err != nil {
			r.step("workers", "", err)
			return
		}
	}
	nc, _, err := daemon.LoadNode(daemon.ConfigDir())
	if err != nil {
		r.step("workers", "", err)
		return
	}
	if nc.NoLaunch {
		r.step("workers", "refused (--no-launch); run setup --launch to accept them again", nil)
		return
	}
	r.step("workers", "accepted (refuse them with --no-launch)", nil)
}

// ---- daemon service ----

func (r *runner) daemonService() {
	if r.NoService {
		r.step("daemon service", "skipped (no service)", nil)
		return
	}
	res, err := r.installService(Service{Name: "daemon", Args: []string{"daemon"}})
	r.step("daemon service", res, err)
}

// ---- Claude Code ----

func claudePath() string { return agentfiles.ClaudePath() }

func runCmd(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (r *runner) claudeMCP() {
	claude := claudePath()
	if claude == "" {
		r.step("claude code", "skipped (claude not found)", nil)
		return
	}
	if _, err := runCmd(home(), claude, "mcp", "get", "agent-net"); err == nil {
		r.step("claude code", "skipped (already registered)", nil)
		return
	}
	if out, err := runCmd(home(), claude, "mcp", "add", "-s", "user", "agent-net", "--", r.Exe, "mcp"); err != nil {
		r.step("claude code", "", fmt.Errorf("claude mcp add: %s", firstLine(out, err)))
		return
	}
	r.step("claude code", "ok", nil)
}

func firstLine(out string, err error) string {
	if out == "" {
		return err.Error()
	}
	return strings.SplitN(out, "\n", 2)[0]
}

// ---- Pi ----

func (r *runner) piExtension() {
	r.stepResult(agentfiles.SyncPi())
	r.stepResult(agentfiles.SyncPiTools())
}

// ---- OpenCode ----

func (r *runner) opencodePlugin() {
	res := agentfiles.SyncOpencode()
	r.stepResult(res)
	r.stepResult(agentfiles.SyncOpencodeTools())
	r.stepResult(agentfiles.SyncAntigravity(r.Exe))
	if res.Err == nil && res.Changed {
		// The service hosts every OpenCode session, so setup never restarts it;
		// the user decides when.
		r.hint("hint: OpenCode 2.2 loads new plugin files by itself; if the agent-net tools are missing in a session, restart its service when no session is busy: opencode service restart")
	}
}

func (r *runner) stepResult(res agentfiles.Result) {
	r.step(res.Name, res.Text, res.Err)
}

// ---- guidance ----

func (r *runner) guidance() {
	for _, res := range agentfiles.SyncGuidance() {
		r.stepResult(res)
	}
}

func displayPath(p string) string { return agentfiles.DisplayPath(p) }

// ---- status ----

func (r *runner) daemonStatus(ctx context.Context) {
	if r.NoService {
		r.step("daemon status", "skipped (no service)", nil)
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	state := "daemon is not reachable (see the service log)"
	for {
		cl, info, err := queryDaemonInfo(ctx)
		if err == nil {
			if info.Connected {
				r.step("daemon status", "connected to "+info.GatewayURL+" as node "+info.NodeID, nil)
				r.statusClient = cl
				r.statusInfo = info
				return
			}
			cl.Close()
			state = "daemon is running but not connected to " + info.GatewayURL + " yet"
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	r.step("daemon status", "not connected: "+state, nil)
}

type daemonInfo struct {
	Connected  bool                   `json:"connected"`
	NodeID     string                 `json:"node_id"`
	Hostname   string                 `json:"hostname"`
	GatewayURL string                 `json:"gateway_url"`
	Launch     bool                   `json:"launch"`
	Sessions   []protocol.SessionInfo `json:"sessions"`
}

func queryDaemonInfo(ctx context.Context) (*client.Client, daemonInfo, error) {
	var info daemonInfo
	cl, err := client.Dial(daemon.SocketPath())
	if err != nil {
		return nil, info, err
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := cl.Call(cctx, "hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol}); err != nil {
		cl.Close()
		return nil, info, err
	}
	raw, err := cl.Call(cctx, "info", nil)
	if err != nil {
		cl.Close()
		return nil, info, err
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		cl.Close()
		return nil, info, err
	}
	return cl, info, nil
}

// ---- helpers ----

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writeIfChanged(path string, data []byte, mode os.FileMode) (changed, existed bool, err error) {
	return agentfiles.WriteIfChanged(path, data, mode)
}

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return ""
}
