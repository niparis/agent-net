package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/upgradecli"
)

// startExe is this binary's path, read before an upgrade can replace it (on
// Linux a replaced binary reads back as "... (deleted)").
var startExe, _ = os.Executable()

// restartGateway runs the gateway binary again in place. It is the gateway's
// restart when no service manager does it (AGENT_NET_NO_SERVICE=1).
func restartGateway() {
	args := append([]string{startExe}, os.Args[1:]...)
	if err := syscall.Exec(startExe, args, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "agent-net: cannot restart the gateway:", err)
		os.Exit(1)
	}
}

func cmdUpgrade(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	ver := fs.String("version", "", "install this release, like v0.2.0 (gateway host only; default: the latest)")
	force := fs.Bool("force", false, "upgrade even if already on that version, or from a dev build")
	check := fs.Bool("check", false, "print versions and change nothing")
	rollback := fs.Bool("rollback", false, "go back to the previous binary on this machine")
	timeout := fs.Duration("timeout", 2*time.Minute, "how long to wait for the other machines")
	data := fs.String("data", defaultGatewayData(), "gateway data directory")
	fs.Parse(args)
	return upgradecli.Run(ctx, upgradecli.Options{
		Version: *ver, Force: *force, Check: *check, Rollback: *rollback, Timeout: *timeout,
		Current: version, GatewayData: *data, Exe: startExe,
	})
}

func cmdNodes(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("nodes", flag.ExitOnError)
	wide := fs.Bool("wide", false, "also list each installed harness with its path, version and models")
	asJSON := fs.Bool("json", false, "print JSON")
	fs.Parse(args)
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, "nodes", nil)
	if err != nil {
		return err
	}
	if *asJSON {
		printJSON(raw)
		return nil
	}
	var info protocol.NodesInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return err
	}
	fmt.Printf("gateway  %s\n\n", orUnknown(info.GatewayVersion))
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "HOST\tNODE\tVERSION\tONLINE\tWORKERS\tHARNESSES")
	for _, n := range info.Nodes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%s\t%s\n", n.Hostname, n.NodeID, orUnknown(n.Version), n.Online, launchSummary(n), harnessSummary(n))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if *wide {
		printHarnessDetails(os.Stdout, info.Nodes)
	}
	return nil
}

func orUnknown(v string) string {
	if v == "" || v == "step2" {
		return "unknown"
	}
	return v
}
