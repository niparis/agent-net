package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/upgrade"
)

type upgradeState struct {
	running atomic.Bool

	mu     sync.Mutex
	target string        // the version the running (or last) install is for
	done   chan struct{} // closed when that install ends
	res    protocol.NodeUpgradeResult
	err    error
}

func (d *Daemon) helloVersion() string {
	if d.cfg.Version == "" {
		return "dev"
	}
	return d.cfg.Version
}

var errUpgradeBusy = errors.New("an upgrade is already running")

// upgradeTo installs target and says whether the daemon must restart to run
// it. It is the one install path, for the gateway's node.upgrade request and
// for automatic upgrades. On success the running flag stays set when the
// daemon is going to restart.
//
// If an install of the same target is already running (or done and waiting for
// the restart), the caller joins it: it gets that install's result and joined
// is true, so it must not restart the daemon a second time. This is what
// happens when an automatic upgrade and the gateway's request meet.
func (d *Daemon) upgradeTo(ctx context.Context, target string) (res protocol.NodeUpgradeResult, joined bool, err error) {
	if !d.upg.running.CompareAndSwap(false, true) {
		d.upg.mu.Lock()
		same, done := d.upg.target == target, d.upg.done
		d.upg.mu.Unlock()
		if !same || done == nil {
			return protocol.NodeUpgradeResult{}, false, errUpgradeBusy
		}
		select {
		case <-done:
		case <-ctx.Done():
			return protocol.NodeUpgradeResult{}, false, ctx.Err()
		}
		d.upg.mu.Lock()
		defer d.upg.mu.Unlock()
		return d.upg.res, true, d.upg.err
	}
	done := make(chan struct{})
	d.upg.mu.Lock()
	d.upg.target, d.upg.done = target, done
	d.upg.mu.Unlock()
	res, err = d.doUpgrade(ctx, target)
	d.upg.mu.Lock()
	d.upg.res, d.upg.err = res, err
	d.upg.mu.Unlock()
	close(done)
	return res, false, err
}

func (d *Daemon) doUpgrade(ctx context.Context, target string) (protocol.NodeUpgradeResult, error) {
	cur := d.helloVersion()
	if cur == "dev" {
		d.upg.running.Store(false)
		return protocol.NodeUpgradeResult{From: cur}, errors.New("this is a dev build, it is not upgraded remotely")
	}
	if target == "" {
		d.upg.running.Store(false)
		return protocol.NodeUpgradeResult{From: cur}, errors.New("no version given")
	}
	_, to, err := d.install(ctx, target)
	if err != nil {
		d.upg.running.Store(false)
		return protocol.NodeUpgradeResult{From: cur}, err
	}
	restarting := to != cur
	if !restarting {
		d.upg.running.Store(false)
	}
	return protocol.NodeUpgradeResult{From: cur, To: to, Restarting: restarting}, nil
}

// onUpgrade handles the gateway's node.upgrade request: install the version,
// answer the gateway, then restart. The answer goes out before the restart so
// the gateway learns the result; the new version shows up in the next hello.
func (d *Daemon) onUpgrade(ctx context.Context, l *link, id, target string) {
	reply := func(res protocol.NodeUpgradeResult, err error) {
		if frame, e := protocol.EncodeResult(id, res, err); e == nil {
			l.write(ctx, frame)
		}
	}
	res, joined, err := d.upgradeTo(ctx, target)
	if err != nil {
		if !errors.Is(err, errUpgradeBusy) {
			d.Logf("upgrade to %s failed: %v", target, err)
		}
		reply(res, err)
		return
	}
	d.Logf("upgrade to %s: installed %s (running %s), restarting: %v, joined an upgrade already running: %v", target, res.To, res.From, res.Restarting, joined)
	reply(res, nil)
	if res.Restarting && !joined {
		go func() {
			time.Sleep(300 * time.Millisecond) // let the answer leave
			d.restart()
		}()
	}
}

// install puts the target version on disk. If the daemon already runs it,
// nothing is downloaded.
func (d *Daemon) install(ctx context.Context, target string) (from, to string, err error) {
	if d.cfg.Upgrade != nil {
		return d.cfg.Upgrade(ctx, target)
	}
	if d.helloVersion() == target {
		return target, target, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	res, err := upgrade.FromGateway(ctx, d.client, d.cfg.GatewayURL, d.cfg.Exe, d.helloVersion(), target)
	return res.From, res.To, err
}

func (d *Daemon) restart() {
	if d.cfg.Restart != nil {
		d.cfg.Restart()
		return
	}
	RestartSelf(d.cfg.Exe, d.Stop)
}

// RestartSelf restarts this daemon process. With a service manager it asks the
// manager: systemd gets `systemctl --user restart` (the unit has Restart=),
// launchd gets an exit and KeepAlive starts it again. Without one (or with
// AGENT_NET_NO_SERVICE=1) the process stops and runs exe again in place.
// stop shuts the daemon down cleanly first.
func RestartSelf(exe string, stop func()) {
	if os.Getenv("AGENT_NET_NO_SERVICE") != "1" {
		if os.Getenv("INVOCATION_ID") != "" {
			if p, err := exec.LookPath("systemctl"); err == nil {
				if exec.Command(p, "--user", "--no-block", "restart", "agent-net-daemon.service").Run() == nil {
					return // systemd stops this process and starts the new binary
				}
			}
		}
		if os.Getenv("XPC_SERVICE_NAME") == "com.agent-net.daemon" {
			stop()
			os.Exit(0) // launchd KeepAlive starts the new binary
		}
	}
	stop()
	args := append([]string{exe}, os.Args[1:]...)
	if err := syscall.Exec(exe, args, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "agent-net: cannot restart:", err)
		os.Exit(1)
	}
}

// checkClientProtocol refuses a local client that is too old for this daemon.
// An old client keeps running until its session restarts, so the message says
// what to do.
func (d *Daemon) checkClientProtocol(clientProto int, clientVersion string) error {
	min := d.cfg.MinLocalProtocol
	if min == 0 {
		min = protocol.MinLocalProtocol
	}
	if clientProto >= min {
		return nil
	}
	who := "this agent-net client"
	if clientVersion != "" {
		who = "agent-net " + clientVersion
	}
	return fmt.Errorf("%s is too old for the daemon (agent-net %s): restart this session so it loads the new version",
		who, d.helloVersion())
}

// dispatchUpgrade serves the local "nodes" and "restart" methods.
func (d *Daemon) dispatchUpgrade(ctx context.Context, method, client string) (any, error) {
	switch method {
	case "nodes":
		return d.call(ctx, "query.nodes", nil)
	case "restart":
		if client != "cli" {
			return nil, errors.New("only the agent-net command line can restart the daemon")
		}
		go func() {
			time.Sleep(200 * time.Millisecond) // let the answer leave
			d.restart()
		}()
		return nil, nil
	}
	return nil, fmt.Errorf("unknown method %q", method)
}

// mcpHelloResult is the answer to the hello of a session's `agent-net mcp`
// process. It also records which version the session's tools are. When they
// are older than this daemon, the answer carries tools_note: one line for the
// process to add, once, to a tool result, so the agent tells its user to
// reconnect agent-net. A process that sent no version is too old to show the
// note; the owner sees it in the TUI instead.
func (d *Daemon) mcpHelloResult(ls *localSession, harness, toolsVersion string) map[string]any {
	res := map[string]any{"node_id": d.cfg.NodeID, "session_id": d.currentSessionID(ls), "version": d.helloVersion()}
	shown, old := toolsVersion, upgrade.IsNewer(d.helloVersion(), toolsVersion)
	if shown == "" {
		shown, old = protocol.ToolsUnknown, true
	}
	d.mu.Lock()
	changed := ls.info.ToolsVersion != shown || ls.info.ToolsOld != old
	ls.info.ToolsVersion, ls.info.ToolsOld = shown, old
	d.mu.Unlock()
	if changed {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.pushSession(ls)
		}()
	}
	if old && toolsVersion != "" {
		res["tools_note"] = protocol.ToolsStaleNote(harness, toolsVersion, d.helloVersion())
	}
	return res
}
