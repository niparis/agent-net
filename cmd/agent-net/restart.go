package main

import (
	"errors"
	"flag"
	"fmt"

	"github.com/niparis/agent-net/internal/setup"
)

// These are replaced in tests so no test restarts a real service.
var (
	restartDaemonService  = setup.RestartDaemon
	restartGatewayService = setup.RestartGateway
)

// cmdRestart restarts this machine's daemon service, and with --gateway the
// gateway service on the gateway machine.
func cmdRestart(args []string) error {
	fs := flag.NewFlagSet("restart", flag.ExitOnError)
	gateway := fs.Bool("gateway", false, "on the gateway machine: also restart the gateway service")
	fs.Parse(args)
	if fs.NArg() != 0 {
		return errors.New("usage: agent-net restart [--gateway]")
	}
	note, err := restartDaemonService()
	if err != nil {
		return err
	}
	fmt.Println(note)
	if !*gateway {
		return nil
	}
	note, err = restartGatewayService()
	if err != nil {
		return err
	}
	fmt.Println(note)
	return nil
}
