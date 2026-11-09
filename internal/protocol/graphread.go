package protocol

// GraphRead is the "graph.read" frame: a daemon asks the gateway for the graph
// of one node, or for all of it. The answer is GraphGot. The graph is visible
// to every enrolled machine (decision 0022), so this is not an owner method.
type GraphRead struct {
	Ref     *GraphRef `json:"ref,omitempty"`
	Typed   bool      `json:"typed,omitempty"`
	Project string    `json:"project,omitempty"` // a node name; empty means all
	// Also asks the gateway to add the other nodes that share Project's name
	// (a repo and an agent can) after the node found by name. Only the public
	// read sets it; the per-turn charter read does not.
	Also bool `json:"also,omitempty"`
}

// GraphGot answers "graph.read": the node's charter and the nodes and edges
// that touch it, or the whole graph when Project was empty.
type GraphGot struct {
	Graph Graph `json:"graph"`
}
