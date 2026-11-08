package protocol

import (
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/settings"
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
		c.FeatureRequests != "" || c.Escalate != "" || c.MaturityNote != "" || c.Rules != ""
}

// CharterBrief is the whole charter, for a worker's brief. Empty when the node
// has no charter.
func CharterBrief(n Node) string { return CharterBriefWithSettings(n, settings.Snapshot{}) }
func CharterBriefWithSettings(n Node, snap settings.Snapshot) string {
	if !charterSet(n.Charter) && NodeDefaultsWithSettings(n.Charter.Maturity, n.Settings, snap, n.Name).RulesText() == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Charter of %s\n", n.Name)
	for _, a := range []struct{ name, value string }{
		{"purpose", n.Charter.Purpose},
		{"maturity", n.Charter.Maturity},
		{"accepts", n.Charter.Accepts},
		{"refuses", n.Charter.Refuses},
		{"feature_requests", n.Charter.FeatureRequests},
		{"escalate", n.Charter.Escalate},
		{"maturity_note", n.Charter.MaturityNote},
		{"rules", n.Charter.Rules},
	} {
		if a.value != "" {
			fmt.Fprintf(&b, "%s: %s\n", a.name, a.value)
		}
	}
	d := NodeDefaultsWithSettings(n.Charter.Maturity, n.Settings, snap, n.Name)
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
	return NodeContextWithSettings(g, name, settings.Snapshot{})
}
func NodeContextWithSettings(g Graph, name string, snap settings.Snapshot) string {
	node := nodeNamed(g, name)
	if node == nil {
		return ""
	}
	if !charterSet(node.Charter) && NodeDefaultsWithSettings(node.Charter.Maturity, node.Settings, snap, node.Name).RulesText() == "" {
		needsDeployApproval := false
		for _, edge := range g.Edges {
			if (edge.Kind == "deploys" || edge.Kind == "delivery_deploys") && edge.From == name {
				if target := nodeNamed(g, edge.To); target != nil && NodeDefaultsWithSettings(target.Charter.Maturity, target.Settings, snap, target.Name).DeployRule == DeployOwner || target != nil && NodeDefaultsWithSettings(target.Charter.Maturity, target.Settings, snap, target.Name).DeployRule == ShorthandCustom {
					needsDeployApproval = true
					break
				}
			}
		}
		if !needsDeployApproval && NeighborCharters(g, name) == "" && DeliveryAssignmentRules(g, name) == "" {
			return ""
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[agent-net] Your effective charter for %s, from agent-net (global -> team -> repo -> agent):\n", node.Name)
	for _, a := range []struct{ name, value string }{
		{"purpose", node.Charter.Purpose},
		{"maturity", node.Charter.Maturity},
		{"maturity_note", node.Charter.MaturityNote},
		{"rules", node.Charter.Rules},
		{"feature_requests", node.Charter.FeatureRequests},
		{"accepts", node.Charter.Accepts},
		{"refuses", node.Charter.Refuses},
		{"escalate", node.Charter.Escalate},
	} {
		if a.value != "" {
			fmt.Fprintf(&b, "%s: %s\n", a.name, a.value)
		}
	}
	d := NodeDefaultsWithSettings(node.Charter.Maturity, node.Settings, snap, node.Name)
	if rules := d.RulesText(); rules != "" {
		b.WriteString(rules + "\n")
		b.WriteString(d.ConsumerText() + "\n")
	}
	// Only the edges that matter to this node: what it depends on, what
	// depends on it, and what it deploys.
	for _, e := range g.Edges {
		switch {
		case e.Kind == "depends" && e.From == name:
			fmt.Fprintf(&b, "You depend on %s.\n", e.To)
		case e.Kind == "depends" && e.To == name:
			fmt.Fprintf(&b, "%s depends on your node.\n", e.From)
		case (e.Kind == "deploys" || e.Kind == "delivery_deploys") && e.From == name:
			// A deployer applies the target's rule, not its own (0022).
			if target := nodeNamed(g, e.To); target != nil {
				td := NodeDefaultsWithSettings(target.Charter.Maturity, target.Settings, snap, target.Name)
				if rules := td.RulesText(); rules != "" {
					fmt.Fprintf(&b, "You deploy %s; its %s.\n", target.Name, rules)
				} else {
					fmt.Fprintf(&b, "You deploy %s; its deploys follow the brief.\n", target.Name)
				}
			}
		}
	}
	if assignments := DeliveryAssignmentRules(g, name); assignments != "" {
		b.WriteString(assignments + "\n")
	}
	if neighbors := NeighborCharters(g, name); neighbors != "" {
		b.WriteString(neighbors + "\n")
	}
	if charterSet(node.Charter) {
		b.WriteString(DecideRule)
	}
	return strings.TrimRight(b.String(), "\n")
}

// DeliveryProjectOwners describes the assigned owners of a project's steps.
// It is also included in worker briefs, whose workers cannot own a step.
func DeliveryProjectOwners(g Graph, project string) string {
	var lines []string
	for _, edge := range g.Edges {
		step := DeliveryEdgeStep(edge.Kind)
		if step != "" && edge.Kind != "deploys" && edge.To == project {
			label := DeliveryEdgeRef(edge.Kind).String()
			if DeliveryOwnerFromSource(edge.From).Kind == OwnerNone {
				continue
			}
			owner := edge.From
			if assignment := DeliveryOwnerFromSource(edge.From); assignment.Kind != OwnerNode {
				owner = assignment.String()
			}
			if step == StepApprove {
				lines = append(lines, fmt.Sprintf("%s is assigned %s for your project, subject to the approval rule.", owner, label))
			} else {
				lines = append(lines, fmt.Sprintf("%s owns %s for your project.", owner, label))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// DeliveryAssignmentRules describes delivery assignments that concern a node.
func DeliveryAssignmentRules(g Graph, name string) string {
	var lines []string
	if project := DeliveryProjectOwners(g, name); project != "" {
		lines = append(lines, project)
	}
	for _, edge := range g.Edges {
		step := DeliveryEdgeStep(edge.Kind)
		if step != "" && edge.Kind != "deploys" && edge.From == name {
			label := DeliveryEdgeRef(edge.Kind).String()
			if step == StepApprove {
				lines = append(lines, fmt.Sprintf("You are assigned %s for %s, subject to its approval rule.", label, edge.To))
			} else {
				lines = append(lines, fmt.Sprintf("You own %s for %s.", label, edge.To))
			}
		}
	}
	return strings.Join(lines, "\n")
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
		}
	}
	return fmt.Sprintf("[agent-net] Relationship: no graph relation between %s and your node %s.", from, to)
}

// WorkerDeployApprovalRules carries the target's written deploy rule into a
// worker brief, including for a deployer with no charter of its own.
func WorkerDeployApprovalRules(g Graph, name string) string {
	return WorkerDeployApprovalRulesWithSettings(g, name, settings.Snapshot{})
}
func WorkerDeployApprovalRulesWithSettings(g Graph, name string, snap settings.Snapshot) string {
	var lines []string
	for _, edge := range g.Edges {
		if (edge.Kind != "deploys" && edge.Kind != "delivery_deploys") || edge.From != name {
			continue
		}
		target := nodeNamed(g, edge.To)
		if target == nil {
			continue
		}
		defaults := NodeDefaultsWithSettings(target.Charter.Maturity, target.Settings, snap, target.Name)
		if defaults.DeployRule == DeployOwner || defaults.DeployRule == ShorthandCustom {
			lines = append(lines, fmt.Sprintf("You deploy %s: %s.", target.Name, defaults.DeployText()))
		}
	}
	return strings.Join(lines, "\n")
}
