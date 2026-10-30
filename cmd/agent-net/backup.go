package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/niparis/agent-net/internal/gateway"
)

func cmdGatewayBackup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("gateway backup", flag.ContinueOnError)
	out := fs.String("out", "", "new absolute directory on the gateway machine")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*out) {
		return errors.New("usage: agent-net gateway backup --out /absolute/new-directory")
	}
	c, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	if _, err = c.Call(ctx, "gateway_backup", map[string]string{"path": *out}); err != nil {
		return err
	}
	fmt.Printf("Backup written to %s on the gateway machine\n", *out)
	return nil
}
func cmdGatewayRestore(args []string) error {
	fs := flag.NewFlagSet("gateway restore", flag.ContinueOnError)
	backup := fs.String("backup", "", "backup directory")
	out := fs.String("to", "", "new gateway data directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *backup == "" || *out == "" {
		return errors.New("usage: agent-net gateway restore --backup DIRECTORY --to NEW_DIRECTORY")
	}
	if err := gateway.RestoreBackup(*backup, *out); err != nil {
		return err
	}
	fmt.Printf("Restored gateway data to %s\n", *out)
	return nil
}
