package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The proposals of the network are the ones on this machine and the ones on
// every other machine that is online. The machines are asked together: when
// they were asked one after the other, the list took the sum of their times,
// and one slow machine held up the rest.
func TestNetworkProposalsAskTheMachinesTogether(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]bool{}
	all := make(chan struct{})
	remotes := []string{"n_remote_a", "n_remote_b", "n_remote_c"}
	f := func(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
		switch typ {
		case "query.nodes":
			var nodes protocol.NodesInfo
			for _, id := range remotes {
				nodes.Nodes = append(nodes.Nodes, protocol.NodeView{NodeID: id, Hostname: id, Online: true})
			}
			out, err := json.Marshal(nodes)
			return out, true, err
		case "query.proposal_list":
			q, _ := params.(protocol.CTOProposalAnswer)
			mu.Lock()
			asked[q.NodeID] = true
			if len(asked) == len(remotes) {
				select {
				case <-all:
				default:
					close(all)
				}
			}
			mu.Unlock()
			select {
			case <-all:
			case <-time.After(1500 * time.Millisecond):
				return nil, true, errors.New("a machine waited for the others: they were asked one after the other")
			}
			item := protocol.CTOProposal{ID: "p_1", NodeID: q.NodeID, Tool: "t", CreatedAt: time.Unix(100, 0).Add(time.Duration(len(q.NodeID)) * time.Second)}
			out, err := json.Marshal([]protocol.CTOProposal{item})
			return out, true, err
		}
		return nil, false, nil
	}
	g := startGateway(t, t.TempDir(), "")
	n := startNode(t, g, nodeOpts{cfg: func(c *Config) { c.testGatewayCall = f }})
	items, err := n.d.networkProposals(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(remotes) {
		t.Fatalf("%d proposals, want one from each machine: %+v", len(items), items)
	}
}
