// Command agent-net connects coding-agent sessions through one gateway.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/niparis/a2a-ocs/internal/client"
	"github.com/niparis/a2a-ocs/internal/daemon"
	"github.com/niparis/a2a-ocs/internal/daemon/claude"
	"github.com/niparis/a2a-ocs/internal/gateway"
	"github.com/niparis/a2a-ocs/internal/mcpserver"
	"github.com/niparis/a2a-ocs/internal/protocol"
)

const usage = `usage: agent-net <command>

  gateway    run the gateway server
  enroll     print a one-time join string (run on the gateway host)
  join       join this machine to a gateway: agent-net join <join-string>
  daemon     run the per-machine daemon
  mcp        stdio MCP server for a Claude Code session
  status     show the daemon's state and local sessions
  list       list agents on the network
  messages   list recent messages and their states
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "gateway":
		err = cmdGateway(ctx, args)
	case "enroll":
		err = cmdEnroll(args)
	case "join":
		err = cmdJoin(ctx, args)
	case "daemon":
		err = cmdDaemon(ctx, args)
	case "mcp":
		err = mcpserver.Run(ctx, daemon.SocketPath())
	case "status":
		err = cmdStatus(ctx, args)
	case "list":
		err = cmdList(ctx, args)
	case "messages":
		err = cmdMessages(ctx, args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent-net:", err)
		os.Exit(1)
	}
}

func defaultGatewayData() string {
	if d := os.Getenv("AGENT_NET_GATEWAY_DATA"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "agent-net", "gateway")
}

func cmdGateway(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("gateway", flag.ExitOnError)
	listen := fs.String("listen", "0.0.0.0:7420", "address to listen on")
	data := fs.String("data", defaultGatewayData(), "data directory")
	sans := fs.String("san", "", "extra host names or IPs for the TLS certificate, comma separated")
	fs.Parse(args)
	var extra []string
	for _, s := range strings.Split(*sans, ",") {
		if s = strings.TrimSpace(s); s != "" {
			extra = append(extra, s)
		}
	}
	logger := log.New(os.Stderr, "gateway: ", log.LstdFlags)
	gw, err := gateway.New(gateway.Options{DataDir: *data, SANs: extra, Logger: logger})
	if err != nil {
		return err
	}
	defer gw.Close()
	go gw.RunExpiry(ctx)
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

func cmdDaemon(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	gw := fs.String("gateway", "", "gateway URL (default: from node.json)")
	dir := fs.String("config-dir", daemon.ConfigDir(), "directory with node.json and ca.pem")
	fs.Parse(args)
	nc, ca, err := daemon.LoadNode(*dir)
	if err != nil {
		return err
	}
	if *gw != "" {
		nc.GatewayURL = strings.TrimRight(*gw, "/")
	}
	host, _ := os.Hostname()
	logger := log.New(os.Stderr, "daemon: ", log.LstdFlags)
	d, err := daemon.New(daemon.Config{
		GatewayURL: nc.GatewayURL, NodeID: nc.NodeID, Token: nc.Token, CAPEM: ca, Hostname: host,
		Adapters: []daemon.Adapter{claude.New(claude.Config{})}, Logger: logger,
	})
	if err != nil {
		return err
	}
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
	if _, err := cl.Call(ctx, "hello", map[string]any{"client": "cli"}); err != nil {
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
	fmt.Printf("gateway  %s (%s)\nnode     %s\n\n", info.GatewayURL, state, info.NodeID)
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
	harness := fs.String("harness", "", "filter by harness")
	text := fs.String("text", "", "free-text filter")
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
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tNAME\tHARNESS\tHOST\tSTATUS\tONLINE\tFUNCTION")
	for _, a := range r.Agents {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%v\t%s\n", a.SessionID, a.Profile.Name, a.Harness, a.Hostname,
			a.Status, a.Online, a.Profile.Function)
	}
	return tw.Flush()
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
