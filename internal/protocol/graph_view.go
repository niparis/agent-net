package protocol

// GraphHistoryPerNode is how many recent charter changes a GraphView carries
// for each node. The map shows the last few; the CTO will read more.
const GraphHistoryPerNode = 5

// QueryGraph asks for a snapshot of the component graph. An empty Project means
// every node. A project name keeps that node and the nodes on an edge with it.
type QueryGraph struct {
	Project string `json:"project,omitempty"`
}

// GraphView is a read-only snapshot of the graph: nodes with their charters,
// the edges, and the newest charter changes per node.
type GraphView struct {
	Nodes   []Node          `json:"nodes"`
	Edges   []Edge          `json:"edges"`
	Changes []CharterChange `json:"changes"`
}
