package protocol

// GraphRead is the "graph.read" frame: a daemon asks the gateway for the graph
// of one node, or for all of it. The answer is GraphGot. The graph is visible
// to every enrolled machine (decision 0022), so this is not an owner method.
type GraphRead struct {
	Project string `json:"project,omitempty"` // a node name; empty means all
}

// GraphGot answers "graph.read": the node's charter and the nodes and edges
// that touch it, or the whole graph when Project was empty.
type GraphGot struct {
	Graph Graph `json:"graph"`
}
