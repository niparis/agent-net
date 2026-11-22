package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const (
	gatewayLayoutOverallTimeout    = 15 * time.Second
	gatewayLayoutPerMachineTimeout = 8 * time.Second
)

// ProjectsLayout asks online machines for their layout and merges the results.
func (g *Gateway) ProjectsLayout(ctx context.Context, p protocol.ProjectsLayoutRequest) (protocol.ProjectsLayoutResult, error) {
	ctx, cancel := context.WithTimeout(ctx, gatewayLayoutOverallTimeout)
	defer cancel()

	nodes, err := g.store.Nodes()
	if err != nil {
		return protocol.ProjectsLayoutResult{Machines: []protocol.MachineLayout{}, Skipped: []protocol.SkippedMachine{}}, err
	}

	if p.Machine != "" {
		var found bool
		for _, n := range nodes {
			if strings.EqualFold(n.Hostname, p.Machine) || strings.EqualFold(n.NodeID, p.Machine) {
				found = true
				break
			}
		}
		if !found {
			names := make([]string, 0, len(nodes))
			for _, n := range nodes {
				names = append(names, n.Hostname)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return protocol.ProjectsLayoutResult{Machines: []protocol.MachineLayout{}, Skipped: []protocol.SkippedMachine{}}, fmt.Errorf("unknown machine %q; no known machines", p.Machine)
			}
			return protocol.ProjectsLayoutResult{Machines: []protocol.MachineLayout{}, Skipped: []protocol.SkippedMachine{}}, fmt.Errorf("unknown machine %q; known machines: %s", p.Machine, strings.Join(names, ", "))
		}
	}

	type target struct {
		machine string
		conn    *nodeConn
	}

	var (
		targets []target
		skipped []protocol.SkippedMachine
	)

	g.mu.Lock()
	for _, n := range nodes {
		if p.Machine != "" && !strings.EqualFold(n.Hostname, p.Machine) && !strings.EqualFold(n.NodeID, p.Machine) {
			continue
		}
		nc := g.conns[n.NodeID]
		if nc == nil {
			skipped = append(skipped, protocol.SkippedMachine{Machine: n.Hostname, Reason: "offline"})
			continue
		}
		targets = append(targets, target{machine: n.Hostname, conn: nc})
	}
	g.mu.Unlock()

	var (
		mu       sync.Mutex
		machines []protocol.MachineLayout
		wg       sync.WaitGroup
	)

	for _, t := range targets {
		wg.Add(1)
		go func(tgt target) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, gatewayLayoutPerMachineTimeout)
			defer cancel()
			data, err := tgt.conn.call(cctx, "node.projects_layout", nil)
			if err != nil {
				mu.Lock()
				skipped = append(skipped, protocol.SkippedMachine{Machine: tgt.machine, Reason: "timeout"})
				mu.Unlock()
				return
			}
			var layout protocol.MachineLayout
			if err := json.Unmarshal(data, &layout); err != nil {
				mu.Lock()
				skipped = append(skipped, protocol.SkippedMachine{Machine: tgt.machine, Reason: "timeout"})
				mu.Unlock()
				return
			}
			if layout.Roots == nil {
				layout.Roots = []protocol.LayoutRoot{}
			}
			if layout.Loose == nil {
				layout.Loose = []protocol.LayoutProject{}
			}
			mu.Lock()
			machines = append(machines, layout)
			mu.Unlock()
		}(t)
	}

	wg.Wait()

	if machines == nil {
		machines = []protocol.MachineLayout{}
	}
	if skipped == nil {
		skipped = []protocol.SkippedMachine{}
	}
	sort.Slice(machines, func(i, j int) bool {
		return machines[i].Machine < machines[j].Machine
	})
	sort.Slice(skipped, func(i, j int) bool {
		return skipped[i].Machine < skipped[j].Machine
	})

	return protocol.ProjectsLayoutResult{Machines: machines, Skipped: skipped}, nil
}
