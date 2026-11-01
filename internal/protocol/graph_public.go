package protocol

import (
	"sort"
	"strings"
)

// The public graph read (owner decision, 2026-11-01). Every agent may read the
// charters and edges of every node. The owner-only parts of a Node stay out:
// its settings, the learned-change history with its evidence, hidden items, and
// anything with machine paths or sessions. The charter itself is public by
// design; the charter guide forbids secrets, addresses, tokens and paths in it.

// Page bounds keep one answer small. The list is one line per node.
const (
	PublicGraphDefaultLimit = 20
	PublicGraphMaxLimit     = 50
	PublicGraphLineChars    = 200
)

// PublicGraphQuery is a public graph read. Node selects one node and returns
// its full charter and the edges that touch it. With no Node, Offset and Limit
// page the node list; all edges come with every page.
type PublicGraphQuery struct {
	Node   string `json:"node,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// PublicGraphNode is one node in the public read. A list line carries Name,
// Kind, Maturity and Line. A detail carries the full Charter as well.
type PublicGraphNode struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind,omitempty"`
	Maturity string   `json:"maturity,omitempty"`
	Line     string   `json:"line,omitempty"`
	Charter  *Charter `json:"charter,omitempty"`
}

// PublicGraphEdge is the public part of an edge: who, what kind, the mode of a
// governs edge, and the request rule.
type PublicGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	Mode string `json:"mode,omitempty"`
	Rule string `json:"rule"`
}

// PublicGraphPage answers a public graph read. Total counts every node that
// matches, Next is the offset of the next page (0 at the end), and EdgeTotal
// counts the edges in Edges.
type PublicGraphPage struct {
	Nodes     []PublicGraphNode `json:"nodes"`
	Edges     []PublicGraphEdge `json:"edges"`
	Total     int               `json:"total"`
	Next      int               `json:"next,omitempty"`
	EdgeTotal int               `json:"edge_total"`
}

// PublicGraph projects a graph onto the public fields only. It never copies a
// node's Settings, so an owner-only setting cannot reach an agent even when
// old or hand-written data holds a value the validator would refuse.
func PublicGraph(g Graph, q PublicGraphQuery) PublicGraphPage {
	page := PublicGraphPage{Nodes: []PublicGraphNode{}, Edges: []PublicGraphEdge{}}
	node := strings.TrimSpace(q.Node)
	if node != "" {
		for _, n := range g.Nodes {
			if n.Name != node {
				continue
			}
			detail := publicNode(n)
			charter := n.Charter
			detail.Charter = &charter
			page.Nodes = append(page.Nodes, detail)
			break
		}
		for _, e := range g.Edges {
			if e.From == node || e.To == node {
				page.Edges = append(page.Edges, publicEdge(e))
			}
		}
		sortPublicEdges(page.Edges)
		page.Total = len(page.Nodes)
		page.EdgeTotal = len(page.Edges)
		return page
	}

	nodes := append([]Node(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	page.Total = len(nodes)
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	if offset > len(nodes) {
		offset = len(nodes)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = PublicGraphDefaultLimit
	}
	if limit > PublicGraphMaxLimit {
		limit = PublicGraphMaxLimit
	}
	end := offset + limit
	if end > len(nodes) {
		end = len(nodes)
	}
	for _, n := range nodes[offset:end] {
		page.Nodes = append(page.Nodes, publicNode(n))
	}
	if end < len(nodes) {
		page.Next = end
	}
	for _, e := range g.Edges {
		page.Edges = append(page.Edges, publicEdge(e))
	}
	sortPublicEdges(page.Edges)
	page.EdgeTotal = len(page.Edges)
	return page
}

func publicNode(n Node) PublicGraphNode {
	out := PublicGraphNode{Name: n.Name, Kind: n.Kind, Maturity: n.Charter.Maturity, Line: publicNodeLine(n)}
	return out
}

// publicNodeLine is the compact list row: name, kind, maturity, purpose, and
// the first part of accepts and refuses.
func publicNodeLine(n Node) string {
	parts := []string{n.Name}
	if n.Kind != "" {
		parts = append(parts, "("+n.Kind+")")
	}
	if n.Charter.Maturity != "" {
		parts = append(parts, "["+n.Charter.Maturity+"]")
	}
	if n.Charter.Purpose != "" {
		parts = append(parts, flattenCharter(n.Charter.Purpose))
	}
	if n.Charter.Accepts != "" {
		parts = append(parts, "accepts: "+shortCharter(n.Charter.Accepts, 60))
	}
	if n.Charter.Refuses != "" {
		parts = append(parts, "refuses: "+shortCharter(n.Charter.Refuses, 60))
	}
	line := strings.Join(parts, " ")
	if r := []rune(line); len(r) > PublicGraphLineChars {
		line = string(r[:PublicGraphLineChars-1]) + "..."
	}
	return line
}

func publicEdge(e Edge) PublicGraphEdge {
	rule := e.Rule
	if rule == "" {
		rule = "normal"
	}
	return PublicGraphEdge{From: e.From, To: e.To, Kind: e.Kind, Mode: e.Mode, Rule: rule}
}

func sortPublicEdges(edges []PublicGraphEdge) {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Mode != b.Mode {
			return a.Mode < b.Mode
		}
		return a.Rule < b.Rule
	})
}

func flattenCharter(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func shortCharter(s string, limit int) string {
	s = flattenCharter(s)
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-1]) + "..."
}
