package protocol

// GraphHistoryPerNode is how many recent charter changes a GraphView carries
// for each node. The map shows the last few; the CTO will read more.
const GraphHistoryPerNode = 5

// QueryGraph asks for a snapshot of the component graph. An empty Project means
// every node. A project name keeps that node and the nodes on an edge with it.
type QueryGraph struct {
	Project string `json:"project,omitempty"`
	All     bool   `json:"all,omitempty"`
}

// GraphView is a read-only snapshot of the graph: nodes with their charters,
// the edges, and the newest charter changes per node.
type GraphView struct {
	Nodes   []Node          `json:"nodes"`
	Edges   []Edge          `json:"edges"`
	Changes []CharterChange `json:"changes"`
}

// GraphTalkCount summarizes messages between two projects in the past week.
type GraphTalkCount struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Count   int    `json:"count"`
	HasEdge bool   `json:"has_edge"`
}

// GraphTalkResult identifies the feed position included in the snapshot.
type GraphTalkResult struct {
	Counts   []GraphTalkCount `json:"counts"`
	Activity map[string]int   `json:"activity"`
	LastID   int64            `json:"last_id"`
}
