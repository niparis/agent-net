package main

import (
	"errors"
	"flag"
	"path/filepath"

	"github.com/niparis/agent-net/internal/gateway"
)

func cmdPhone(args []string) error {
	if len(args) == 0 || args[0] != "owner" {
		return errors.New("usage: agent-net phone owner [--data DIR] <login>")
	}
	fs := flag.NewFlagSet("phone owner", flag.ContinueOnError)
	data := fs.String("data", defaultGatewayData(), "gateway data directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: agent-net phone owner [--data DIR] <login>")
	}
	s, err := gateway.OpenStore(filepath.Join(*data, "gateway.db"))
	if err != nil {
		return err
	}
	defer s.Close()
	return s.SetPhoneOwner(fs.Arg(0))
}
