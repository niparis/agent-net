package gateway

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// ProjectsLayout asks online machines for their layout and merges the results.
func (g *Gateway) ProjectsLayout(ctx context.Context, p protocol.ProjectsLayoutRequest) (protocol.ProjectsLayoutResult, error) {
	nodes, err := g.store.Nodes()
	if err != nil {
		return protocol.ProjectsLayoutResult{Machines: []protocol.MachineLayout{}}, err
	}

	type target struct {
		id   string
		name string
		conn *nodeConn
	}

	g.mu.Lock()
	var targets []target
	for _, n := range nodes {
		if p.Machine != "" && !strings.EqualFold(n.Hostname, p.Machine) && !strings.EqualFold(n.NodeID, p.Machine) {
			continue
		}
		if nc, ok := g.conns[n.NodeID]; ok && nc != nil {
			targets = append(targets, target{id: n.NodeID, name: n.Hostname, conn: nc})
		}
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
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			data, err := tgt.conn.call(cctx, "node.projects_layout", nil)
			if err != nil {
				return
			}
			var layout protocol.MachineLayout
			if err := json.Unmarshal(data, &layout); err == nil {
				mu.Lock()
				machines = append(machines, layout)
				mu.Unlock()
			}
		}(t)
	}

	wg.Wait()

	if machines == nil {
		machines = []protocol.MachineLayout{}
	}
	sort.Slice(machines, func(i, j int) bool {
		return machines[i].Machine < machines[j].Machine
	})

	return protocol.ProjectsLayoutResult{Machines: machines}, nil
}
