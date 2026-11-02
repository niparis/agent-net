package protocol_test

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/guidance"
	"github.com/niparis/agent-net/internal/protocol"
)

// Assemble all agent-net text visible beside an auto-edge request. The same
// receiver rule must appear in the delivery and in its per-turn context.
func TestDeliveredMergeRulesAgree(t *testing.T) {
	for _, authority := range []string{"full", "delegated", "ask"} {
		t.Run(authority, func(t *testing.T) {
			g := protocol.Graph{Nodes: []protocol.Node{{Name: "receiver", Charter: protocol.Charter{Maturity: "mature"}, Settings: map[string]string{protocol.SettingMergeAuthority: authority}}}, Edges: []protocol.Edge{{From: "sender", To: "receiver", Kind: "depends", Rule: "auto"}}}
			d := protocol.NodeDefaults("mature", g.Nodes[0].Settings)
			// Both notes use only the receiver setting supplied by the gateway.
			note := protocol.AuthorityNote(true, false, authority)
			owner := protocol.OwnerInstructionNote("ask receiver to merge example/repo#12", authority)
			delivery := strings.Join([]string{note, protocol.RelationText(g, "sender", "receiver"), protocol.NodeContext(g, "receiver"), owner, guidance.Block(guidance.Other)}, "\n")
			if !strings.Contains(note, d.MergeText()) {
				t.Errorf("delivery trust note lacks receiver merge rule %q: %s", d.MergeText(), note)
			}
			if !strings.Contains(owner, "On its own this does not approve a merge.") || !strings.Contains(owner, d.MergeText()) {
				t.Errorf("owner instruction must point to receiver rule: %s", owner)
			}
			for _, other := range []string{protocol.MergeFullText, protocol.MergeDelegatedText, protocol.MergeAskText} {
				if other != d.MergeText() && strings.Contains(delivery, other) {
					t.Errorf("contradicting merge rule %q in delivery", other)
				}
			}
			if strings.Count(delivery, d.MergeText()) != 3 {
				t.Errorf("expected one consistent rule in trust, context and owner notes: %s", delivery)
			}
			if strings.Contains(delivery, "or merging a pull request into the default branch") {
				t.Error("unconditional merge consent contradicts receiver settings")
			}
			if authority == "delegated" && (!strings.Contains(delivery, "agent_owner_ask") || !strings.Contains(d.MergeText(), "governs")) {
				t.Error("delegated approval must name its source and delivery path")
			}
			if !strings.Contains(guidance.Block(guidance.Other), "For merges, follow the receiving node's merge rule in agent-net's note") {
				t.Error("guidance must defer merge consent to receiver settings")
			}
		})
	}
}

func TestMergeOverrideWithoutMaturityHasContext(t *testing.T) {
	g := protocol.Graph{Nodes: []protocol.Node{{Name: "receiver", Settings: map[string]string{protocol.SettingMergeAuthority: protocol.MergeDelegated}}}}
	if got := protocol.NodeContext(g, "receiver"); !strings.Contains(got, protocol.MergeDelegatedText) {
		t.Fatalf("override missing from context: %q", got)
	}
}
