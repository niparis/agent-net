package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/niparis/a2a-ocs/internal/setup"
)

func cmdSetup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	join := fs.String("join", "", "join string from `agent-net invite` or `agent-net enroll`")
	gw := fs.Bool("gateway", false, "also install and start the gateway on this machine")
	noService := fs.Bool("no-service", false, "do not write or start services")
	data := fs.String("data", defaultGatewayData(), "gateway data directory (with --gateway)")
	dist := fs.String("dist", setup.DefaultDist(), "directory the gateway serves binaries from (with --gateway)")
	fs.Parse(args)
	return setup.Setup(ctx, setup.Options{Join: *join, Gateway: *gw, NoService: *noService, Data: *data, Dist: *dist})
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
