package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// cmdNodesRename: agent-net nodes rename <node-id|name> [new name]. It is the
// scriptable form of the N key in the screen (decision 0015). No new name, or
// an empty one, goes back to the host name the machine reports.
func cmdNodesRename(ctx context.Context, args []string) error {
	return runNodesRename(ctx, opClient(), os.Stdout, args)
}

func runNodesRename(ctx context.Context, op operator.Operator, out io.Writer, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: agent-net nodes rename <node-id|current name> [new name]   (no new name clears it)")
	}
	name := ""
	if len(args) == 2 {
		name = args[1]
	}
	if _, err := protocol.CleanNodeName(name); err != nil {
		return err
	}
	snap, err := op.Snapshot(ctx)
	if err != nil {
		return err
	}
	id, err := pickNode(snap.Nodes, args[0])
	if err != nil {
		return err
	}
	res, err := op.RenameNode(ctx, id, name)
	if err != nil {
		return err
	}
	if res.DisplayName == "" {
		fmt.Fprintf(out, "%s is called %s again (the name it reports)\n", res.NodeID, res.Hostname)
		return nil
	}
	fmt.Fprintf(out, "%s is now called %s on every machine\n", res.NodeID, res.Hostname)
	return nil
}

// pickNode finds a node by its ID or by a name it is listed under: the name
// shown, the owner's name, or the name it reports. Case does not matter for a name.
func pickNode(nodes []protocol.NodeView, want string) (string, error) {
	var hits []string
	for _, n := range nodes {
		switch {
		case n.NodeID == want:
			return n.NodeID, nil
		case strings.EqualFold(n.Hostname, want), strings.EqualFold(n.DisplayName, want), strings.EqualFold(n.Reported, want):
			hits = append(hits, n.NodeID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("no machine %q (see `agent-net nodes`)", want)
	case 1:
		return hits[0], nil
	}
	return "", fmt.Errorf("%d machines match %q: use the node ID (%s)", len(hits), want, strings.Join(hits, ", "))
}
