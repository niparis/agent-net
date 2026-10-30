package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

func workerProfileArgs(args []string) (string, protocol.WorkerProfileRequest, error) {
	var p protocol.WorkerProfileRequest
	if len(args) == 0 {
		return protocol.OpWorkerProfiles, p, nil
	}
	switch args[0] {
	case "add":
		if len(args) == 3 || len(args) == 4 {
			p.Harness, p.Model = args[1], args[2]
			if len(args) == 4 {
				p.Effort = args[3]
			}
			return protocol.OpWorkerProfileAdd, p, nil
		}
	case "remove":
		if len(args) == 2 && strings.TrimSpace(args[1]) != "" {
			p.Name = args[1]
			return protocol.OpWorkerProfileRemove, p, nil
		}
	}
	return "", p, errors.New("usage: agent-net workers profiles [add <harness> <model> [effort] | remove <name>]")
}
func cmdWorkerProfiles(ctx context.Context, args []string) error {
	method, p, err := workerProfileArgs(args)
	if err != nil {
		return err
	}
	cl, err := dialCLI(ctx)
	if err != nil {
		return err
	}
	defer cl.Close()
	raw, err := cl.Call(ctx, method, p)
	if err != nil {
		return err
	}
	if method == protocol.OpWorkerProfiles {
		var profiles []protocol.WorkerProfile
		if err := json.Unmarshal(raw, &profiles); err != nil {
			return err
		}
		if len(profiles) == 0 {
			fmt.Println("no registered workers")
		}
		for _, p := range profiles {
			fmt.Printf("%s  %s  %s", p.Name, p.Harness, p.Model)
			if p.Effort != "" {
				fmt.Printf(":%s", p.Effort)
			}
			fmt.Printf("  added by %s\n", p.AddedBy)
		}
		return nil
	}
	if method == protocol.OpWorkerProfileRemove {
		fmt.Printf("Removed worker profile %s.\n", p.Name)
		return nil
	}
	var out protocol.WorkerProfile
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	fmt.Printf("Registered worker profile %s.\n", out.Name)
	return nil
}
