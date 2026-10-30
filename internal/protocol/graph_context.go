package protocol

import (
	"fmt"
	"strings"
)

// Charter context (decision 0022). A session speaks for the node of its
// project. Every turn it gets a short text: its charter, the edges that matter
// to it, and the rule to decide instead of defer. A worker's brief gets the
// whole charter. Nothing is written for a node without a charter.

// DecideRule is the rule that goes into every per-turn charter context and
// into the guidance block in internal/guidance.
const DecideRule = "Decide, do not defer: judge every request from another agent against this charter and decide (accept, reject with a reason, or redirect). " +
	"Escalate only when the charter says so, or when it does not cover the request. Every escalation carries a recommendation."

// charterSet tells whether a node has any charter at all.
func charterSet(c Charter) bool {
	return c.Purpose != "" || c.Maturity != "" || c.Accepts != "" || c.Refuses != "" ||
		c.Consumers != "" || c.Escalate != "" || c.Needs != "" || c.Notes != ""
}

// CharterBrief is the whole charter, for a worker's brief. Empty when the node
// has no charter.
func CharterBrief(n Node) string {
	if !charterSet(n.Charter) && NodeDefaults(n.Charter.Maturity, n.Settings).RulesText() == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Charter of %s\n", n.Name)
	for _, a := range []struct{ name, value string }{
		{"purpose", n.Charter.Purpose},
		{"maturity", n.Charter.Maturity},
		{"accepts", n.Charter.Accepts},
		{"refuses", n.Charter.Refuses},
		{"consumers", n.Charter.Consumers},
		{"escalate", n.Charter.Escalate},
		{"needs", n.Charter.Needs},
		{"notes", n.Charter.Notes},
	} {
		if a.value != "" {
			fmt.Fprintf(&b, "%s: %s\n", a.name, a.value)
		}
	}
	d := NodeDefaults(n.Charter.Maturity, n.Settings)
	if rules := d.RulesText(); rules != "" {
		b.WriteString(rules + "\n")
		b.WriteString(d.ConsumerText() + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// nodeNamed returns the node with this name from a graph, or nil.
func nodeNamed(g Graph, name string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].Name == name {
			return &g.Nodes[i]
		}
	}
	return nil
}

// NodeContext is the short text a session of the node gets every turn. It is
// empty when the node is unknown or has no charter.
func NodeContext(g Graph, name string) string {
	node := nodeNamed(g, name)
	if node == nil {
		return ""
	}
	if !charterSet(node.Charter) && NodeDefaults(node.Charter.Maturity, node.Settings).RulesText() == "" {
		needsDeployApproval := false
		for _, edge := range g.Edges {
			if edge.Kind == "deploys" && edge.From == name {
				if target := nodeNamed(g, edge.To); target != nil && NodeDefaults(target.Charter.Maturity, target.Settings).DeployRule == DeployOwner {
					needsDeployApproval = true
					break
				}
			}
		}
		if !needsDeployApproval {
			return ""
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[agent-net] Your node's charter for %s, from agent-net:\n", node.Name)
	for _, a := range []struct{ name, value string }{
		{"maturity", node.Charter.Maturity},
		{"accepts", node.Charter.Accepts},
		{"refuses", node.Charter.Refuses},
		{"escalate", node.Charter.Escalate},
	} {
		if a.value != "" {
			fmt.Fprintf(&b, "%s: %s\n", a.name, a.value)
		}
	}
	d := NodeDefaults(node.Charter.Maturity, node.Settings)
	if rules := d.RulesText(); rules != "" {
		b.WriteString(rules + "\n")
		b.WriteString(d.ConsumerText() + "\n")
	}
	// Only the edges that matter to this node: who governs it, what it
	// depends on, what depends on it, and, for an architect, what it governs.
	for _, e := range g.Edges {
		switch {
		case e.Kind == "governs" && e.To == name:
			fmt.Fprintf(&b, "%s governs your node as %s.\n", e.From, e.Mode)
		case e.Kind == "depends" && e.From == name:
			fmt.Fprintf(&b, "You depend on %s.\n", e.To)
		case e.Kind == "depends" && e.To == name:
			fmt.Fprintf(&b, "%s depends on your node.\n", e.From)
		case e.Kind == "governs" && e.From == name:
			fmt.Fprintf(&b, "You govern %s as %s.\n", e.To, e.Mode)
		case e.Kind == "deploys" && e.From == name:
			// A deployer applies the target's rule, not its own (0022).
			if target := nodeNamed(g, e.To); target != nil {
				td := NodeDefaults(target.Charter.Maturity, target.Settings)
				if rules := td.RulesText(); rules != "" {
					fmt.Fprintf(&b, "You deploy %s; its %s.\n", target.Name, rules)
				} else {
					fmt.Fprintf(&b, "You deploy %s; its deploys follow the brief.\n", target.Name)
				}
			}
		}
	}
	if charterSet(node.Charter) {
		b.WriteString(DecideRule)
	}
	return strings.TrimRight(b.String(), "\n")
}

// RelationText says how the sender's node relates to the recipient's node, for
// the line the gateway adds after a delivered request. Empty when there is no
// pair of distinct nodes, or when no edge says anything.
func RelationText(g Graph, from, to string) string {
	if from == "" || to == "" || from == to {
		return ""
	}
	for _, e := range g.Edges {
		if e.From != from || e.To != to {
			continue
		}
		switch e.Kind {
		case "depends":
			return fmt.Sprintf("[agent-net] Relationship: %s is a consumer of your node %s (it depends on you).", from, to)
		case "governs":
			return fmt.Sprintf("[agent-net] Relationship: %s governs your node %s as %s.", from, to, e.Mode)
		}
	}
	return fmt.Sprintf("[agent-net] Relationship: no graph relation between %s and your node %s.", from, to)
}

// WorkerDeployApprovalRules carries the target's written deploy rule into a
// worker brief, including for a deployer with no charter of its own.
func WorkerDeployApprovalRules(g Graph, name string) string {
	var lines []string
	for _, edge := range g.Edges {
		if edge.Kind != "deploys" || edge.From != name {
			continue
		}
		target := nodeNamed(g, edge.To)
		if target == nil {
			continue
		}
		defaults := NodeDefaults(target.Charter.Maturity, target.Settings)
		if defaults.DeployRule == DeployOwner {
			lines = append(lines, fmt.Sprintf("You deploy %s: %s.", target.Name, defaults.DeployText()))
		}
	}
	return strings.Join(lines, "\n")
}
