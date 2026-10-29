package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/niparis/agent-net/internal/setup"
)

func cmdSetup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	join := fs.String("join", "", "join string from `agent-net invite` or `agent-net enroll`")
	gw := fs.Bool("gateway", false, "also install and start the gateway on this machine")
	noService := fs.Bool("no-service", false, "do not write or start services")
	noLaunch := fs.Bool("no-launch", false, "refuse workers on this machine: other sessions cannot start agent sessions here")
	launch := fs.Bool("launch", false, "accept workers again after --no-launch")
	data := fs.String("data", defaultGatewayData(), "gateway data directory (with --gateway)")
	dist := fs.String("dist", setup.DefaultDist(), "directory the gateway serves binaries from (with --gateway)")
	port := fs.String("port", setup.GatewayPort, "gateway port (with --gateway)")
	accept := fs.Bool("accept-peer-messages", false, "set Claude Code crossSessionInbound to accept without asking (only if unset)")
	noAccept := fs.Bool("no-accept-peer-messages", false, "do not set crossSessionInbound and do not ask")
	fs.Parse(args)
	if *accept && *noAccept {
		return errors.New("--accept-peer-messages and --no-accept-peer-messages cannot be used together")
	}
	peer := ""
	if *accept {
		peer = setup.PeerMessagesAccept
	} else if *noAccept {
		peer = setup.PeerMessagesNo
	}
	return setup.Setup(ctx, setup.Options{PeerMessages: peer, Join: *join, Gateway: *gw, NoService: *noService, NoLaunch: *noLaunch, Launch: *launch, Data: *data, Dist: *dist, Port: *port})
}

func cmdUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also remove credentials and daemon state")
	noService := fs.Bool("no-service", false, "do not touch services")
	fs.Parse(args)
	return setup.Uninstall(setup.Options{Purge: *purge, NoService: *noService})
}

func cmdInvite(args []string) error {
	fs := flag.NewFlagSet("invite", flag.ExitOnError)
	data := fs.String("data", defaultGatewayData(), "gateway data directory")
	host := fs.String("host", "", "host name or IP that machines use to reach the gateway (default: the default-route address)")
	port := fs.String("port", "7420", "gateway port")
	fs.Parse(args)
	lines, err := setup.Invite(setup.InviteOptions{Host: *host, Port: *port, Data: *data})
	if err != nil {
		return err
	}
	for _, l := range lines {
		if len(lines) > 1 {
			fmt.Printf("# %s\n", l[0])
		}
		fmt.Println(l[1])
	}
	if len(lines) > 1 {
		fmt.Fprintln(os.Stderr, "Use the line that matches how the other machine reaches this one.")
	}
	fmt.Fprintln(os.Stderr, "Valid for 10 minutes, works for one machine. Paste it in a shell on the machine to add (Linux or macOS).")
	return nil
}
