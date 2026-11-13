package protocol

import (
	"fmt"
	"sort"
	"strings"
)

// The public graph read (owner decision, 2026-11-01). Every agent may read the
// charters, rules and edges of every node, read-only. The rules are the node's
// effective settings (decision 0022) and its delegation policies (decision
// 0014). Only changes are owner-gated. What stays hidden is not a rule: a
// secret or token, a machine path, the learned-change history with its evidence
// and the owner's hidden-items preferences. The charter itself is public by
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
	Ref    *GraphRef `json:"ref,omitempty"`
	Typed  bool      `json:"typed,omitempty"`
	Node   string    `json:"node,omitempty"`
	Offset int       `json:"offset,omitempty"`
	Limit  int       `json:"limit,omitempty"`
	// Root limits the read to one subtree of the org tree.
	Root string `json:"root,omitempty"`
}

// PublicRule is one node rule an agent follows: the setting name and its
// effective value. It is read-only; only the owner changes a setting.
type PublicRule struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// PublicGraphNode is one node in the public read. A list line carries Name,
// Kind, Maturity, Line and Rules. A detail carries the full Charter and the
// node's delegation policies as well.
type PublicGraphNode struct {
	Active     *bool            `json:"active,omitempty"`
	Ref        *GraphRef        `json:"ref,omitempty"`
	Owner      *GraphRef        `json:"owner,omitempty"`
	Lead       *GraphRef        `json:"lead,omitempty"`
	MaxAgents  int              `json:"max_agents,omitempty"`
	AgentCount int              `json:"agent_count,omitempty"`
	OverMax    bool             `json:"over_max,omitempty"`
	Role       string           `json:"role,omitempty"`
	LeadLevel  string           `json:"lead_level,omitempty"`
	Name       string           `json:"name"`
	Kind       string           `json:"kind,omitempty"`
	Maturity   string           `json:"maturity,omitempty"`
	Line       string           `json:"line,omitempty"`
	Rules      []PublicRule     `json:"rules,omitempty"`
	Delegation []NodeDelegation `json:"delegation,omitempty"`
	Charter    *Charter         `json:"charter,omitempty"`
	Loop       *LoopView        `json:"loop,omitempty"`
	// The org tree of an Agent node; see Node.
	ReportsTo   string         `json:"reports_to,omitempty"`
	LeadSession *GraphSession  `json:"lead_session,omitempty"`
	Sessions    []GraphSession `json:"sessions,omitempty"`
	Scopes      []Scope        `json:"scopes,omitempty"`
}

// PublicGraphEdge is the public part of an edge: who, what kind, and the request
// rule.
type PublicGraphEdge struct {
	FromRef *GraphRef `json:"-"`
	ToRef   *GraphRef `json:"-"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Kind    string    `json:"kind"`
	Mode    string    `json:"mode,omitempty"`
	Rule    string    `json:"rule"`
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
	// Note is set when a name read matches more than one node.
	Note string `json:"note,omitempty"`
}

// PublicGraph projects a graph onto the public fields only. It never copies a
// node's Settings, so an owner-only setting cannot reach an agent even when
// old or hand-written data holds a value the validator would refuse.
func PublicGraph(g Graph, q PublicGraphQuery) PublicGraphPage {
	page := PublicGraphPage{Nodes: []PublicGraphNode{}, Edges: []PublicGraphEdge{}}
	node := strings.TrimSpace(q.Node)
	if node != "" || q.Ref != nil {
		for _, n := range g.Nodes {
			if (q.Ref != nil && !RefMatches(n.Ref, q.Ref)) || (q.Ref == nil && n.Name != node) {
				continue
			}
			detail := publicNode(n)
			charter := n.Charter
			detail.Charter = &charter
			page.Nodes = append(page.Nodes, detail)
			if q.Ref != nil {
				break
			}
		}
		for _, e := range g.Edges {
			if (q.Ref != nil && (RefMatches(e.FromRef, q.Ref) || RefMatches(e.ToRef, q.Ref))) || (q.Ref == nil && (e.From == node || e.To == node)) {
				page.Edges = append(page.Edges, publicEdge(e))
			}
		}
		sortPublicEdges(page.Edges)
		page.Total = len(page.Nodes)
		page.EdgeTotal = len(page.Edges)
		if q.Ref == nil && len(page.Nodes) > 1 {
			kinds := make([]string, len(page.Nodes))
			for i, n := range page.Nodes {
				kinds[i] = n.Kind
			}
			page.Note = fmt.Sprintf("%d nodes are named %q (%s). Each is a separate node with its own charter; read one alone with typed: true and its ref.", len(page.Nodes), node, strings.Join(kinds, ", "))
		}
		return page
	}

	nodes := append([]Node(nil), g.Nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Name != nodes[j].Name {
			return nodes[i].Name < nodes[j].Name
		}
		if nodes[i].Kind != nodes[j].Kind {
			return nodes[i].Kind < nodes[j].Kind
		}
		if nodes[i].Ref != nil && nodes[j].Ref != nil {
			return nodes[i].Ref.ID < nodes[j].Ref.ID
		}
		return false
	})
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
	return PublicGraphNode{
		Ref: n.Ref, Owner: n.Owner, Lead: n.Lead, MaxAgents: n.MaxAgents, AgentCount: n.AgentCount, OverMax: n.OverMax, Role: n.Role, Active: n.Active,
		LeadLevel:  n.LeadLevel,
		Name:       n.Name,
		Kind:       n.Kind,
		Maturity:   n.Charter.Maturity,
		Line:       publicNodeLine(n),
		Rules:      publicRules(n),
		Delegation: publicDelegation(n.Delegation),
		Loop:       n.Loop,

		ReportsTo: n.ReportsTo, LeadSession: n.LeadSession, Sessions: n.Sessions, Scopes: n.Scopes,
	}
}

// publicRules returns the node's effective rules as name/value pairs. Only the
// six known setting names are read, and only through NodeDefaults and
// NodeAutonomy, which ignore an unknown name or an invalid value. A secret or
// path stored under any other setting name therefore never reaches an agent.
func publicRules(n Node) []PublicRule {
	if n.Kind == "agent" || n.Kind == "team" {
		return nil
	}
	return RulesFor(n.Charter.Maturity, n.Settings)
}

// RulesFor returns a node's effective rules as public name/value pairs. It is
// also what agent_session_get puts on a session's node.
func RulesFor(maturity string, settings map[string]string) []PublicRule {
	d := NodeDefaults(maturity, settings)
	return []PublicRule{
		{Name: SettingTestsBeforeMerge, Value: d.TestsBeforeMerge},
		{Name: SettingMergeAuthority, Value: d.MergeAuthority},
		{Name: SettingWorkerSandbox, Value: d.WorkerSandbox},
		{Name: SettingJudgeConsumers, Value: d.JudgeConsumers},
		{Name: SettingDeployRule, Value: d.DeployRule},
		{Name: SettingAutonomy, Value: NodeAutonomy(settings)},
	}
}

// publicDelegation copies only valid, non-empty delegation policies. A policy
// with an invalid value (old or hand-written data) is dropped, like an invalid
// node setting.
func publicDelegation(in []NodeDelegation) []NodeDelegation {
	out := make([]NodeDelegation, 0, len(in))
	for _, d := range in {
		clean, err := d.DelegationPolicy.Clean()
		if err != nil || clean.Empty() {
			continue
		}
		out = append(out, NodeDelegation{Machine: d.Machine, DelegationPolicy: clean})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Machine < out[j].Machine })
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
	return PublicGraphEdge{FromRef: e.FromRef, ToRef: e.ToRef, From: e.From, To: e.To, Kind: e.Kind, Mode: e.Mode, Rule: rule}
}

func sortPublicEdges(edges []PublicGraphEdge) {
	sort.Slice(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		if a.FromRef != nil && b.FromRef != nil && a.FromRef.Kind != b.FromRef.Kind {
			return a.FromRef.Kind < b.FromRef.Kind
		}
		if a.ToRef != nil && b.ToRef != nil && a.ToRef.Kind != b.ToRef.Kind {
			return a.ToRef.Kind < b.ToRef.Kind
		}
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
