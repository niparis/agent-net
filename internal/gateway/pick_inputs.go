package gateway

import "github.com/niparis/agent-net/internal/protocol"

// pickerInputs holds everything an automatic pick reads from the store before
// it builds its roster: the owner's policy, the model facts, the workers that
// run on the node and the owner's quota reading with their burn.
type pickerInputs struct {
	profiles  []protocol.WorkerProfile
	providers protocol.WorkerProviders
	minimums  map[string]float64
	protected protocol.ProtectedProviders
	floors    protocol.WorkerFloors
	scores    []protocol.ModelScore
	agents    []protocol.AgentView
	// running counts the online workers by quota provider on the node.
	running map[string]int
	// quotas is the owner's reading with the burn of the running workers.
	quotas *protocol.Quotas
	// onlyHarness keeps the roster to the harness the caller named; empty is
	// the whole roster.
	onlyHarness string
}

// pickInputs loads the inputs one automatic pick reads, once. A start, a
// continue re-pick and the difficulty judge all decide on the same set, so
// they load it here instead of reading each store again.
func (g *Gateway) pickInputs(node *protocol.NodeView, onlyHarness string) (pickerInputs, error) {
	in := pickerInputs{onlyHarness: onlyHarness}
	var err error
	if in.profiles, err = g.store.WorkerProfiles(); err != nil {
		return in, err
	}
	if in.providers, err = g.store.workerProviders(); err != nil {
		return in, err
	}
	if in.minimums, err = g.store.difficultyMinimums(); err != nil {
		return in, err
	}
	if in.protected, err = g.store.protectedProviders(); err != nil {
		return in, err
	}
	if in.floors, err = g.store.workerFloors(); err != nil {
		return in, err
	}
	if in.scores, err = g.store.scores(); err != nil {
		return in, err
	}
	if in.agents, err = g.store.Agents(protocol.QueryAgents{OnlineOnly: true}); err != nil {
		return in, err
	}
	in.running = map[string]int{}
	for _, a := range in.agents {
		if a.NodeID == node.NodeID && a.Worker != nil {
			in.running[protocol.QuotaProviderForStart(a.Harness, a.Model)]++
		}
	}
	in.quotas = g.withQuotaBurn(g.ownerQuotas(node.NodeID), in.agents)
	return in, nil
}

// candidates builds the roster of this pick from the loaded inputs.
func (in pickerInputs) candidates(g *Gateway, facts *candidateFacts, roster *notStartedRoster, node *protocol.NodeView, readOnly bool) ([]protocol.WorkerModelCandidate, []string, map[string]string, error) {
	return g.workerModelCandidatesWith(facts, roster, node, in.profiles, in.providers, in.quotas, in.onlyHarness, readOnly, in.protected)
}
