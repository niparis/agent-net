package mcpserver

// The graph read tool: agent_graph_read (tool redesign task 2b).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

type roGraphReadIn struct {
	Ref    *protocol.GraphRef `json:"ref,omitempty"`
	Typed  bool               `json:"typed,omitempty"`
	Node   string             `json:"node"`
	Limit  int                `json:"limit"`
	Cursor string             `json:"cursor"`
}

type roGraphNode struct {
	Active     *bool                 `json:"active,omitempty"`
	Ref        *protocol.GraphRef    `json:"ref,omitempty"`
	Owner      *protocol.GraphRef    `json:"owner,omitempty"`
	Lead       *protocol.GraphRef    `json:"lead,omitempty"`
	MaxAgents  int                   `json:"max_agents,omitempty"`
	AgentCount int                   `json:"agent_count,omitempty"`
	OverMax    bool                  `json:"over_max,omitempty"`
	Role       string                `json:"role,omitempty"`
	LeadLevel  string                `json:"lead_level,omitempty"`
	Name       string                `json:"name"`
	Kind       string                `json:"kind"`
	Maturity   string                `json:"maturity"`
	Purpose    string                `json:"purpose"`
	Charter    *protocol.ReadCharter `json:"charter,omitempty"`
	Rules      map[string]string     `json:"rules"`
}

type roGraphEdge struct {
	From any    `json:"from"`
	To   any    `json:"to"`
	Kind string `json:"kind"`
	Mode string `json:"mode"`
	Rule string `json:"rule"`
}

type roGraphReadOut struct {
	Nodes      []roGraphNode `json:"nodes"`
	Edges      []roGraphEdge `json:"edges"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Note       string        `json:"note,omitempty"`
}

func addGraphReadTool(s *mcp.Server, call Caller) {
	roAdd(s, "agent_graph_read", func(ctx context.Context, _ *mcp.CallToolRequest, in roGraphReadIn) (*mcp.CallToolResult, roGraphReadOut, error) {
		out, err := roReadGraph(ctx, call, in)
		return nil, out, err
	})
}

func roReadGraph(ctx context.Context, call Caller, in roGraphReadIn) (roGraphReadOut, error) {
	limit, err := roLimit(in.Limit)
	if err != nil {
		return roGraphReadOut{}, err
	}
	filters := roFilterKey(struct {
		Node  string
		Ref   *protocol.GraphRef
		Typed bool
	}{in.Node, in.Ref, in.Typed})
	after, err := roDecodeCursor(in.Cursor, "agent_graph_read", roCursorCaller(ctx), filters)
	if err != nil {
		return roGraphReadOut{}, err
	}
	if in.Node != "" || in.Ref != nil {
		page, err := roGraphPage(ctx, call, in.Node, 0, 0, in.Ref, in.Typed)
		if err != nil {
			return roGraphReadOut{}, err
		}
		out := roGraphReadOut{Nodes: roGraphNodes(page.Nodes), Edges: roGraphEdges(page.Edges)}
		out.Note = page.Note
		if len(out.Nodes) == 0 {
			out.Note = fmt.Sprintf("No graph node is named %q. Read the graph without node to list the names.", in.Node)
		}
		return out, nil
	}

	nodes, edges, err := roAllGraphNodes(ctx, call, in.Typed)
	if err != nil {
		return roGraphReadOut{}, err
	}
	nodeKey := func(node roGraphNode) string {
		if node.Ref != nil {
			return node.Name + "\x00" + node.Ref.Kind + "\x00" + node.Ref.ID
		}
		return node.Name
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodeKey(nodes[i]) < nodeKey(nodes[j]) })
	page, next := roWindow(nodes, limit, after, false, nodeKey)
	out := roGraphReadOut{Nodes: page, Edges: edges}
	if next != "" {
		out.NextCursor = roEncodeCursor(roCursor{Tool: "agent_graph_read", Caller: roCursorCaller(ctx), Filters: filters, After: next})
		out.Note = roPageNote("graph nodes")
	}
	return out, nil
}

func roGraphPage(ctx context.Context, call Caller, node string, offset, limit int, ref *protocol.GraphRef, typed bool) (protocol.PublicGraphPage, error) {
	raw, err := call(ctx, "agent_graph", protocol.PublicGraphQuery{Node: node, Offset: offset, Limit: limit, Ref: ref, Typed: typed})
	if err != nil {
		return protocol.PublicGraphPage{}, err
	}
	var page protocol.PublicGraphPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return protocol.PublicGraphPage{}, err
	}
	return page, nil
}

// roAllGraphNodes reads every node. The daemon pages the node list; every page
// carries all edges.
func roAllGraphNodes(ctx context.Context, call Caller, typed bool) ([]roGraphNode, []roGraphEdge, error) {
	var nodes []roGraphNode
	var edges []roGraphEdge
	offset := 0
	for page := 0; page < 1000; page++ {
		got, err := roGraphPage(ctx, call, "", offset, protocol.PublicGraphMaxLimit, nil, typed)
		if err != nil {
			return nil, nil, err
		}
		if page == 0 {
			edges = roGraphEdges(got.Edges)
		}
		nodes = append(nodes, roGraphNodes(got.Nodes)...)
		if got.Next <= 0 || len(got.Nodes) == 0 {
			break
		}
		offset = got.Next
	}
	if edges == nil {
		edges = []roGraphEdge{}
	}
	return nodes, edges, nil
}

func roGraphNodes(nodes []protocol.PublicGraphNode) []roGraphNode {
	out := make([]roGraphNode, 0, len(nodes))
	for _, node := range nodes {
		purpose, maturity := "", node.Maturity
		var charter *protocol.ReadCharter
		if node.Charter != nil {
			purpose = node.Charter.Purpose
			if node.Charter.Maturity != "" {
				maturity = node.Charter.Maturity
			}
			charter = &protocol.ReadCharter{
				Maturity: node.Charter.Maturity, MaturityNote: node.Charter.MaturityNote,
				Escalate: node.Charter.Escalate, Rules: node.Charter.Rules, Sources: node.Charter.Sources,
				Purpose:         node.Charter.Purpose,
				Accepts:         roCharterList(node.Charter.Accepts),
				Refuses:         roCharterList(node.Charter.Refuses),
				FeatureRequests: node.Charter.FeatureRequests,
				WorkMode:        node.Charter.WorkMode,
			}
		}
		rules := map[string]string{}
		for _, rule := range node.Rules {
			rules[rule.Name] = rule.Value
		}
		out = append(out, roGraphNode{Active: node.Active, Ref: node.Ref, Owner: node.Owner, Lead: node.Lead, MaxAgents: node.MaxAgents, AgentCount: node.AgentCount, OverMax: node.OverMax, Role: node.Role, LeadLevel: node.LeadLevel, Name: node.Name, Kind: node.Kind, Maturity: maturity, Purpose: purpose, Charter: charter, Rules: rules})
	}
	return out
}

func roCharterList(value string) []string {
	return protocol.SplitCharterList(value)
}

func roGraphEdges(edges []protocol.PublicGraphEdge) []roGraphEdge {
	out := make([]roGraphEdge, 0, len(edges))
	for _, edge := range edges {
		var from, to any = edge.From, edge.To
		if edge.FromRef != nil {
			from = edge.FromRef
		}
		if edge.ToRef != nil {
			to = edge.ToRef
		}
		out = append(out, roGraphEdge{From: from, To: to, Kind: edge.Kind, Mode: edge.Mode, Rule: edge.Rule})
	}
	return out
}
