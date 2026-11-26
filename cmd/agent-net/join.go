package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/setup"
)

// cmdJoin joins a new machine with a join string, or points an already joined
// machine at a gateway that moved to another address.
func cmdJoin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	dir := fs.String("config-dir", daemon.ConfigDir(), "where node.json and ca.pem are")
	gw := fs.String("gateway", "", "on a machine that already joined: use this gateway URL, keeping its node ID and token")
	fs.Parse(args)
	if *gw != "" {
		if fs.NArg() != 0 {
			return errors.New("usage: agent-net join --gateway URL")
		}
		old, next, err := daemon.ChangeGateway(ctx, *gw, *dir)
		if err != nil {
			return err
		}
		note, err := setup.RestartDaemon()
		if err != nil {
			return fmt.Errorf("gateway changed from %s to %s, but the daemon did not restart: %v", old, next, err)
		}
		fmt.Printf("gateway changed from %s to %s (%s)\n", old, next, note)
		return nil
	}
	if fs.NArg() != 1 {
		return errors.New("usage: agent-net join <join-string>, or agent-net join --gateway URL on a machine that already joined")
	}
	nc, err := daemon.Join(ctx, fs.Arg(0), *dir)
	if err != nil {
		return err
	}
	fmt.Printf("joined %s as node %s (credentials in %s)\n", nc.GatewayURL, nc.NodeID, *dir)
	return nil
}
