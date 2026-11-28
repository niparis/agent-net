package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/niparis/agent-net/internal/daemon"
	"github.com/niparis/agent-net/internal/macsign"
)

// cmdMacosSign signs a freshly downloaded binary with this Mac's own identity
// (decision 0199). install.sh and the gateway's join script run it with the
// new binary on itself, before they put it in place, and go on when it fails.
// On other systems it does nothing.
func cmdMacosSign(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: agent-net macos-sign <binary>")
	}
	if err := macsign.Sign(ctx, daemon.StateDir(), args[0]); err != nil {
		return fmt.Errorf("%s is not signed: %w", args[0], err)
	}
	return nil
}
