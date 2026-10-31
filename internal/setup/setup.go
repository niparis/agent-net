package setup

import (
	"bytes"
	"context"
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

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// GatewayPort is the port the gateway service listens on.
const GatewayPort = "7420"

// Options configure Setup and Uninstall.
type Options struct {
	Join         string // join string; empty means keep the current credentials
	Gateway      bool   // also install and start the gateway on this machine
	NoService    bool   // do not write or start services
	NoLaunch     bool   // refuse workers on this machine (saved in node.json)
	Launch       bool   // accept workers again after --no-launch
	Purge        bool   // uninstall only: also remove credentials and state
	Data         string // gateway data dir
	Dist         string // gateway dist dir
	Port         string // gateway port (default 7420)
	Exe          string // path of the agent-net binary; default: this binary
	All          string // "" ask; AllYes or AllNo answer every integration without a prompt
	PeerMessages string // "" leave to All/network; PeerMessagesAccept or PeerMessagesNo is a local answer
	CodexTools   string // "" leave; CodexToolsApprove or CodexToolsNo is a local answer
	HintHook     string // "" leave; HintHookYes or HintHookNo is a local answer
	PolicyHook   string // "" leave; PolicyHookYes or PolicyHookNo is a local answer
	NeedsHook    string // "" leave; NeedsHookYes or NeedsHookNo is a local answer
	WorkersMod   string // "" leave; WorkersModYes or WorkersModNo is a local answer
	QuotaAXI     string // "" leave; QuotaAXIYes or QuotaAXINo is a local answer
	Out          io.Writer
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
	failed error
	// want is the answer for the seven integrations. When it is set, the steps
	// apply it and turn an item off as well as on. When it is nil, the steps
	// keep their old ask-once behavior.
	want *protocol.Integrations
}

func newRunner(o Options) (*runner, error) {
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
	return &runner{Options: o}, nil
}

// step prints one result line. A non-nil err prints as a failure.
func (r *runner) step(name, result string, err error) {
	if err != nil {
		result = "failed (" + err.Error() + ")"
		if r.failed == nil {
			r.failed = fmt.Errorf("%s: %w", name, err)
		}
	}
	fmt.Fprintf(r.Out, "%-21s %s\n", name, result)
}

func (r *runner) hint(format string, a ...any) {
	fmt.Fprintf(r.Out, "%-21s %s\n", "", fmt.Sprintf(format, a...))
}

// Setup makes this machine part of the network. It prints one line per step.
func Setup(ctx context.Context, o Options) error {
	r, err := newRunner(o)
	if err != nil {
		return err
	}
	if r.Gateway {
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
	r.daemonService()
	r.chooseIntegrations(ctx)
	r.proposeProtectedProviders(ctx)
	r.claudeMCP()
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

func (r *runner) setupGateway(ctx context.Context) {
	data := r.Data
	dist := r.Dist
	if dist == "" {
		dist = DefaultDist()
	}
	if r.NoService {
		r.step("gateway service", "skipped (no service)", nil)
	} else {
		args := []string{"gateway", "--listen", "0.0.0.0:" + r.port(), "--dist", dist}
		if data != "" {
			args = append(args, "--data", data)
		}
		res, err := r.installService(Service{Name: "gateway", Args: args})
		r.step("gateway service", res, err)
	}
	res, err := r.syncDist(dist)
	r.step("gateway binaries", res, err)
}

// syncDist makes sure the dist dir has this binary for its own platform.
func (r *runner) syncDist(dist string) (string, error) {
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
	if joinStr == "" {
		if !r.NoService {
			if err := waitForPort("localhost:"+r.port(), 10*time.Second); err != nil {
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
		joinStr = fmt.Sprintf("https://localhost:%s#%s#%s", r.port(), code, fp)
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

func claudePath() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	h := home()
	for _, p := range []string{
		filepath.Join(h, ".local", "bin", "claude"),
		filepath.Join(h, ".claude", "local", "claude"),
		"/opt/homebrew/bin/claude",
		"/usr/local/bin/claude",
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}

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
}

// ---- OpenCode ----

func (r *runner) opencodePlugin() {
	res := agentfiles.SyncOpencode()
	r.stepResult(res)
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
		if info, err := queryInfo(ctx); err == nil {
			if info.Connected {
				r.step("daemon status", "connected to "+info.GatewayURL+" as node "+info.NodeID, nil)
				return
			}
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
	Connected  bool   `json:"connected"`
	NodeID     string `json:"node_id"`
	GatewayURL string `json:"gateway_url"`
}

func queryInfo(ctx context.Context) (daemonInfo, error) {
	var info daemonInfo
	cl, err := client.Dial(daemon.SocketPath())
	if err != nil {
		return info, err
	}
	defer cl.Close()
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := cl.Call(cctx, "hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol}); err != nil {
		return info, err
	}
	raw, err := cl.Call(cctx, "info", nil)
	if err != nil {
		return info, err
	}
	return info, json.Unmarshal(raw, &info)
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
