// Command agent-net connects coding-agent sessions through one gateway.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/niparis/agent-net/internal/diagnostics"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/niparis/agent-net/internal/agentfiles"
	"github.com/niparis/agent-net/internal/client"
	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/daemon/claude"
	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/logrotate"
	"github.com/niparis/agent-net/internal/mcpserver"
	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/phone"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/setup"
	"github.com/niparis/agent-net/internal/tui"
	"github.com/niparis/agent-net/internal/upgrade"
	sysusage "github.com/niparis/agent-net/internal/usage"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `usage: agent-net <command>

  gateway    run the gateway server
  setup      set this machine up: --join S, --gateway, --no-service, --[no-]auto-upgrade, --all, --none, --[no-]accept-peer-messages, --[no-]accept-codex-tools, --[no-]claude-hint-hook, --[no-]claude-policy-hook, --[no-]claude-needs-hook, --[no-]claude-workers-mod, --quota, --[no-]quota-axi
  invite     print the one-line installer for a new machine (run on the gateway host)
  uninstall  remove services and integrations (--purge also removes credentials)
  enroll     print a one-time join string (run on the gateway host)
  join       join this machine to a gateway: agent-net join <join-string>
  daemon     run the per-machine daemon
  mcp        stdio MCP server for a Claude Code session; with a subcommand (add, list, test,
             remove, enable, disable) manages the MCP server registry
  status     show the daemon's state and local sessions
  list       list agents on the network
  nodes      list machines with their agent-net version and installed harnesses
             (nodes rename <node-id|name> [new name] sets a display name)
  tools      show observed skills and MCP servers
  projects   list known projects (registered and seen); add, scan and remove register them
  events     show the gateway's events table (run on the gateway machine)
  tui        live terminal view of every agent session on the network; start sessions, send as yourself, stop workers
  upgrade    upgrade agent-net (--check, --rollback, --version, --force)
  messages   list recent messages and their states
  start      start a worker session: --project NAME --harness opencode --model M "<task>"
  stop       stop a worker: agent-net stop <session>
  workers    list the workers on the network with their owners
  test-run   run a local test suite through this machine's admission:
             agent-net test-run [--timeout D] -- <command...>
  cto        show or change CTO harness and model settings
  charter    show, set or list the history of a node's charter
  graph      show the graph or undo a change
  node       add or remove an architect
  merger     set or clear a project merger registration
  edge       set or remove an edge
  send       send a message as yourself: agent-net send <session or name> <message...>
  scores     model scores and worker providers: scores providers [provider ...|--default]
  playbook   the owner's notes about a model: playbook set <model> [--harness H] [--pin] <text>,
             playbook get <model> [--harness H], playbook pin|unpin <model> [--harness H]
  version    print the version of this binary
`

// These are replaced in tests: they decide whether bare `agent-net` opens the
// TUI, and run it through the same command as `agent-net tui`.
var (
	stdinIsTerminal  = func() bool { return isTerminal(os.Stdin) }
	stdoutIsTerminal = func() bool { return isTerminal(os.Stdout) }
	runTUI           = cmdTUI
)

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stderr))
}

// run dispatches a command line and returns the process exit code. With no
// arguments on a terminal it opens the TUI, so the owner only has to learn
// `agent-net setup` and `agent-net`. Without a terminal it prints the usage
// and exits 2, so scripts see no change.
func run(ctx context.Context, argv []string, stderr io.Writer) int {
	args := argv
	if len(args) == 0 {
		if !stdinIsTerminal() || !stdoutIsTerminal() {
			fmt.Fprint(stderr, usage)
			return 2
		}
		args = []string{"tui"}
	}
	var err error
	cmd := args[0]
	args = args[1:]
	switch cmd {
	case "gateway":
		err = cmdGateway(ctx, args)
	case "setup":
		err = cmdSetup(ctx, args)
	case "invite":
		err = cmdInvite(args)
	case "uninstall":
		err = cmdUninstall(args)
	case "enroll":
		err = cmdEnroll(args)
	case "join":
		err = cmdJoin(ctx, args)
	case "daemon":
		err = cmdDaemon(ctx, args)
	case "cto-mcp":
		if len(args) != 0 {
			err = errors.New("cto-mcp takes no arguments")
		} else {
			err = mcpserver.RunCTO(ctx, daemon.SocketPath(), version)
		}
	case "tool-decide": // hidden: the Claude Code PreToolUse hook that allows delegated calls (decision 0165)
		err = cmdToolDecide(ctx)
	case "cto-hook": // hidden alias: a CTO session launched before v0.5.56 still runs it, and it does what tool-decide does. It may go once no running CTO can have been launched before v0.5.56.
		err = cmdToolDecide(ctx)
	case "hook":
		err = cmdAntigravityHook(args)
	case "mcp":
		// No subcommand: the stdio server Claude Code starts. Anything else is the registry CLI.
		if len(args) == 0 {
			err = mcpserver.Run(ctx, daemon.SocketPath(), mcpserver.Options{Version: version,
				Exe: startExe, NoAutoUpgrade: os.Getenv("AGENT_NET_NO_AUTO_UPGRADE") == "1"})
		} else if len(args) == 1 && args[0] == "--antigravity" {
			err = mcpserver.RunAntigravity(ctx, daemon.SocketPath(), mcpserver.Options{Version: version, Exe: startExe, NoAutoUpgrade: os.Getenv("AGENT_NET_NO_AUTO_UPGRADE") == "1"})
		} else if len(args) == 1 && args[0] == "--codex" {
			// The stdio server Codex starts, once per thread.
			err = mcpserver.RunCodex(ctx, daemon.SocketPath(), mcpserver.Options{Version: version,
				Exe: startExe, NoAutoUpgrade: os.Getenv("AGENT_NET_NO_AUTO_UPGRADE") == "1"})
		} else {
			err = cmdMCP(ctx, args)
		}
	case "status":
		err = cmdStatus(ctx, args)
	case "list":
		err = cmdList(ctx, args)
	case "nodes":
		err = cmdNodes(ctx, args)
	case "tools":
		err = cmdTools(ctx, args)
	case "projects":
		err = cmdProjects(ctx, args)
	case "events":
		err = cmdEvents(ctx, args)
	case "tui":
		err = runTUI(ctx, args)
	case "upgrade":
		err = cmdUpgrade(ctx, args)
	case "messages":
		err = cmdMessages(ctx, args)
	case "start":
		err = cmdStart(ctx, args)
	case "stop":
		err = cmdStop(ctx, args)
	case "workers":
		err = cmdWorkers(ctx, args)
	case "test-run":
		err = cmdTestRun(ctx, args)
	case "cto":
		err = cmdCTO(ctx, args)
	case "charter", "graph", "node", "edge", "merger":
		err = cmdGraph(ctx, cmd, args)
	case "send":
		err = cmdSend(ctx, args)
	case "scores":
		err = cmdScores(ctx, args)
	case "playbook":
		err = cmdPlaybook(ctx, args)
	case "hint": // hidden: the Claude Code hook (decision 0013)
		err = cmdHint(ctx, args)
	case "policy-hook": // hidden: the Claude Code delegation policy hook (decision 0014)
		err = cmdPolicyHook(ctx, args)
	case "needs-hook": // hidden: the Claude Code "needs you" hook (decision 0016)
		err = cmdNeedsHook(ctx, args)
	case "dev": // hidden: developer tools for tuning agent-net itself
		err = cmdDev(ctx, args)
	case "version", "--version":
		fmt.Println(version)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		var exit exitCodeError
		if errors.As(err, &exit) {
			return exit.code
		}
		fmt.Fprintln(stderr, "agent-net:", err)
		return 1
	}
	return 0
}

func defaultGatewayData() string {
	if d := os.Getenv("AGENT_NET_GATEWAY_DATA"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "agent-net", "gateway")
}

// serviceLog is where a service writes its log lines. On macOS launchd points
// stdout and stderr at ~/Library/Logs/agent-net-<name>.log and never rotates
// that file, so the process writes through a writer that renames the file by
// size. A terminal or a pipe (Linux journald, a manual run, tests) is left
// alone.
func serviceLog(name string) io.Writer {
	path := logrotate.ServicePath(name)
	if path == "" || !logrotate.IsFile(os.Stderr, path) {
		return os.Stderr
	}
	w, err := logrotate.Open(path, logrotate.MaxBytes, logrotate.Keep)
	if err != nil {
		return os.Stderr
	}
	return w
}

func cmdGateway(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "backup" {
		return cmdGatewayBackup(ctx, args[1:])
	}
	if len(args) > 0 && args[0] == "restore" {
		return cmdGatewayRestore(args[1:])
	}
	fs := flag.NewFlagSet("gateway", flag.ExitOnError)
	listen := fs.String("listen", "0.0.0.0:7420", "address to listen on")
	appsListen := fs.String("apps-listen", "", "override the managed apps listener address (default 127.0.0.1:7422)")
	appsPublicURL := fs.String("apps-public-url", "", "public base URL of the apps listener")
	data := fs.String("data", defaultGatewayData(), "data directory")
	sans := fs.String("san", "", "extra host names or IPs for the TLS certificate, comma separated")
	dist := fs.String("dist", setup.DefaultDist(), "directory with the agent-net-{os}-{arch} binaries served at /install/bin")
	keep := gateway.DefaultRetention()
	fs.DurationVar(&keep.Messages, "keep-messages", keep.Messages, "delete message bodies and delivery records older than this (0 keeps them for ever)")
	fs.DurationVar(&keep.Events, "keep-events", keep.Events, "delete events older than this (0 keeps them for ever)")
	fs.DurationVar(&keep.Sessions, "keep-sessions", keep.Sessions, "delete offline sessions not seen for this long (0 keeps them for ever)")
	fs.DurationVar(&keep.WorkerRuns, "keep-worker-runs", keep.WorkerRuns, "delete worker runs behind the model track record older than this (0 keeps them for ever)")
	fs.Parse(args)
	if keep.Messages < 0 || keep.Events < 0 || keep.Sessions < 0 || keep.WorkerRuns < 0 {
		return errors.New("the --keep-* periods must not be negative")
	}
	var extra []string
	for _, s := range strings.Split(*sans, ",") {
		if s = strings.TrimSpace(s); s != "" {
			extra = append(extra, s)
		}
	}
	// Every log line carries its own ts=, so the logger adds nothing.
	logger := log.New(serviceLog("gateway"), "", 0)
	if profiling := diagnostics.StartPprof(os.Getenv("AGENT_NET_PPROF"), logger); profiling != nil {
		defer profiling.Close()
	}
	// The phone app is served by the gateway-managed apps listener.
	op := operator.NewPhone(daemon.SocketPath())
	defer op.Close()
	gw, err := gateway.New(gateway.Options{PhoneApp: phone.New(op), DataDir: *data, DistDir: *dist, SANs: extra, Logger: logger, PublicURL: *appsPublicURL, AppsListen: *appsListen,
		Version: version, Restart: restartGateway, Retention: keep})
	if err != nil {
		return err
	}
	defer gw.Close()
	if err := gw.WriteAdminInfo(*listen); err != nil {
		return err
	}
	go gw.RunExpiry(ctx)
	go gw.RunLearning(ctx)
	go gw.RunPrices(ctx, gateway.OpenRouterModelsURL)
	go gw.RunRetention(ctx)
	go gw.RunGraphCutover(ctx)
	go gw.RunExposure(ctx, *listen)
	srv := &http.Server{Addr: *listen, Handler: gw.Handler(), TLSConfig: gw.TLSConfig()}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	fmt.Printf("gateway listening on https://%s\nCA SHA-256: %s\n", *listen, gw.Fingerprint())
	if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func cmdEnroll(args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	data := fs.String("data", defaultGatewayData(), "gateway data directory")
	host := fs.String("host", "", "host name or IP that nodes use to reach the gateway (default: this host's name)")
	port := fs.String("port", "7420", "gateway port")
	fs.Parse(args)
	fp, err := gateway.CAFingerprint(*data)
	if err != nil {
		return err
	}
	if *host == "" {
		*host, _ = os.Hostname()
	}
	st, err := gateway.OpenStore(filepath.Join(*data, "gateway.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	code, err := st.CreateEnrollCode(10 * time.Minute)
	if err != nil {
		return err
	}
	fmt.Printf("https://%s:%s#%s#%s\n", *host, *port, code, fp)
	fmt.Fprintln(os.Stderr, "valid for 10 minutes, usable once. Run on the other machine: agent-net join '<the line above>'")
	return nil
}

func cmdJoin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	dir := fs.String("config-dir", daemon.ConfigDir(), "where to save node.json and ca.pem")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: agent-net join <join-string>")
	}
	nc, err := daemon.Join(ctx, fs.Arg(0), *dir)
	if err != nil {
		return err
	}
	fmt.Printf("joined %s as node %s (credentials in %s)\n", nc.GatewayURL, nc.NodeID, *dir)
	return nil
}

// envDuration reads a duration like "0s" from the environment. 0s becomes a
// negative value, which the daemon reads as "no wait".
func envDuration(name string) time.Duration {
	d, err := time.ParseDuration(os.Getenv(name))
	if err != nil {
		return 0
	}
	if d == 0 {
		return -1
	}
	return d
}

func cmdDaemon(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	gw := fs.String("gateway", "", "gateway URL (default: from node.json)")
	dir := fs.String("config-dir", daemon.ConfigDir(), "directory with node.json and ca.pem")
	noAuto := fs.Bool("no-auto-upgrade", false, "do not upgrade to a newer version the gateway serves (or set AGENT_NET_NO_AUTO_UPGRADE=1)")
	fs.Parse(args)
	nc, ca, err := daemon.LoadNode(*dir)
	if err != nil {
		return err
	}
	if *gw != "" {
		nc.GatewayURL = strings.TrimRight(*gw, "/")
	}
	ws, err := nc.WorkerSettings()
	if err != nil {
		return err
	}
	host := daemon.Hostname(nc.Hostname)
	logger := log.New(serviceLog("daemon"), "daemon: ", log.LstdFlags)
	if profiling := diagnostics.StartPprof(os.Getenv("AGENT_NET_PPROF"), logger); profiling != nil {
		defer profiling.Close()
	}
	inventoryHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	d, err := daemon.New(daemon.Config{
		InventoryHome: inventoryHome, NodeConfigDir: daemon.ConfigDir(),
		GatewayURL: nc.GatewayURL, NodeID: nc.NodeID, Token: nc.Token, CAPEM: ca, Hostname: host,
		GatewayData: defaultGatewayData(),
		Adapters:    []daemon.Adapter{claude.New(claude.Config{})}, Logger: logger, Version: version,
		CodexHome:         agentfiles.CodexHome(),
		AgySummaries:      filepath.Join(inventoryHome, ".gemini", "antigravity-cli", "conversation_summaries.db"),
		RefreshAgentFiles: agentfiles.SyncAndLogWithSnapshot,
		ApplyIntegrations: setup.ApplyIntegrations,
		UsageSampler:      sysusage.New(nil),
		NoAutoUpgrade:     nc.NoAutoUpgrade || *noAuto || os.Getenv("AGENT_NET_NO_AUTO_UPGRADE") == "1",
		NoLaunch:          nc.NoLaunch, ModelProviders: nc.ModelProviders, Workers: ws, EnterSSH: nc.EnterSSHEnabled(), EnterSSHHosts: nc.EnterSSHHosts,
		QuotaAXIPath: nc.QuotaAXI,
		// Tests set this to 0s so they need not wait up to 10 s.
		AutoUpgradeMaxDelay: envDuration("AGENT_NET_AUTO_UPGRADE_MAX_DELAY"),
	})
	if err != nil {
		return err
	}
	setup.RefreshDaemonUnit()
	if err := d.Start(ctx); err != nil {
		return err
	}
	logger.Printf("running, socket %s, gateway %s", daemon.SocketPath(), nc.GatewayURL)
	<-ctx.Done()
	d.Stop()
	return nil
}

// ---- inspection commands ----

func dialCLI(ctx context.Context) (*client.Client, error) {
	cl, err := client.Dial(daemon.SocketPath())
	if err != nil {
		return nil, fmt.Errorf("cannot reach the daemon at %s (is `agent-net daemon` running?): %w", daemon.SocketPath(), err)
	}
	if _, err := cl.Call(ctx, "hello", map[string]any{"client": "cli", "protocol": protocol.LocalProtocol}); err != nil {
		cl.Close()
		return nil, err
	}
	return cl, nil
}

func printJSON(raw json.RawMessage) {
	var v any
	json.Unmarshal(raw, &v)
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func cmdStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, "info", nil)
	if err != nil {
		return err
	}
	if *asJSON {
		printJSON(raw)
		return nil
	}
	var info struct {
		Connected  bool                   `json:"connected"`
		Version    string                 `json:"version"`
		NodeID     string                 `json:"node_id"`
		GatewayURL string                 `json:"gateway_url"`
		Sessions   []protocol.SessionInfo `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return err
	}
	state := "disconnected"
	if info.Connected {
		state = "connected"
	}
	fmt.Printf("version  %s\ngateway  %s (%s)\nnode     %s\n", info.Version, info.GatewayURL, state, info.NodeID)
	if note := upgrade.LoadAuto(daemon.StateDir()).HoldNote(); note != "" {
		fmt.Printf("upgrade  %s\n", note)
	}
	fmt.Println()
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tHARNESS\tNAME\tSTATUS\tCWD")
	for _, s := range info.Sessions {
		id := s.SessionID
		if id == "" {
			id = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", id, s.Harness, s.Name, s.Status, s.Cwd)
	}
	return tw.Flush()
}

func cmdList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	all := fs.Bool("all", false, "include offline agents")
	dormant := fs.Bool("dormant", false, "include dormant agents (reachable, but quiet for a long time)")
	harness := fs.String("harness", "", "filter by harness")
	text := fs.String("text", "", "free-text filter")
	wide := fs.Bool("wide", false, "also show branch, remote, folder and node")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, "list", protocol.QueryAgents{Harness: *harness, Text: *text, OnlineOnly: !*all})
	if err != nil {
		return err
	}
	if *asJSON {
		printJSON(raw)
		return nil
	}
	var r struct {
		Agents []protocol.AgentView `json:"agents"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	return printAgents(os.Stdout, withoutDormant(r.Agents, *dormant), *wide)
}

func cmdMessages(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("messages", flag.ExitOnError)
	session := fs.String("session", "", "only messages of this session")
	limit := fs.Int("limit", 20, "how many")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, "messages", protocol.QueryMessages{SessionID: *session, Limit: *limit})
	if err != nil {
		return err
	}
	if *asJSON {
		printJSON(raw)
		return nil
	}
	var r struct {
		Messages []protocol.Message `json:"messages"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tMESSAGE\tSTATE\tTYPE\tFROM\tTO\tBODY")
	for _, m := range r.Messages {
		body := strings.Join(strings.Fields(m.Body), " ")
		if len(body) > 50 {
			body = body[:50] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", m.CreatedAt.Local().Format("15:04:05"), m.ID, m.State,
			m.Type, m.From, m.To, body)
	}
	return tw.Flush()
}

// cmdTUI shows the network live in the terminal. It talks to the local daemon
// through the operator interface, so it runs on any machine of the network.
func cmdTUI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	fs.Parse(args)
	if fi, err := os.Stdout.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return errors.New("tui needs a terminal; use `agent-net list --json` in a script")
	}
	return tui.Run(ctx, tui.NewDaemonSource(daemon.SocketPath()), version)
}
