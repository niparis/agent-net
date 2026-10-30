package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

func cmdCTO(ctx context.Context, args []string) error {
	return runCTOCommand(ctx, opClient(), os.Stdout, args)
}
func runCTOCommand(ctx context.Context, op graphCaller, out io.Writer, args []string) error {
	var req protocol.CTOSettingsRequest
	if len(args) != 0 {
		if len(args) != 2 {
			return fmt.Errorf("usage: agent-net cto [harness <opencode|claude>|model <model>]")
		}
		switch args[0] {
		case "harness":
			if args[1] != "opencode" && args[1] != "claude" {
				return fmt.Errorf("harness must be opencode or claude")
			}
			req.Harness = &args[1]
		case "model":
			req.Model = &args[1]
		default:
			return fmt.Errorf("usage: agent-net cto [harness <opencode|claude>|model <model>]")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, operator.Timeout)
	defer cancel()
	raw, err := op.Call(ctx, protocol.OpCTOSettings, req)
	if err != nil {
		return err
	}
	var settings protocol.CTOSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return err
	}
	b, err := json.Marshal(settings.Compaction)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "harness: %s\nmodel: %s\ncompaction: %s\n", settings.Harness, settings.Model, b)
	return nil
}
