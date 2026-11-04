package guidance

import (
	"encoding/json"

	"github.com/niparis/agent-net/internal/settings"
	"strings"
	"testing"
)

func TestVariantsDifferOnlyInReplyLine(t *testing.T) {
	c, o := strings.Split(Block(Claude), "\n"), strings.Split(Block(Other), "\n")
	if len(c) != len(o) {
		t.Fatal("different line counts")
	}
	diff := 0
	for i := range c {
		if c[i] != o[i] {
			diff++
			if !strings.Contains(c[i], "SendMessage") || strings.Contains(o[i], "SendMessage") {
				t.Errorf("unexpected difference: %q vs %q", c[i], o[i])
			}
		}
	}
	if diff != 1 {
		t.Fatalf("%d lines differ, want 1", diff)
	}
	if strings.Contains(Block(Claude), "{{") || strings.Contains(Block(Claude), "\xe2\x80\x94") {
		t.Error("placeholder or em dash left in block")
	}
}

// Announcing the task is a rule, not a hint, for every variant.
func TestAnnounceRuleIsMandatory(t *testing.T) {
	for _, v := range []string{Claude, Other} {
		b := Block(v)
		for _, want := range []string{
			"Announce your task. This is required.",
			"At the start of every session, before other work, call `agent_announce` with your current task in one short line.",
			"Call it again whenever your task changes.",
			"AGENTS.md automatically",
			"When you stop to wait for my decision or answer, call `agent_owner_ask` with the question in one line",
		} {
			if !strings.Contains(b, want) {
				t.Errorf("%s block lacks %q", v, want)
			}
		}
		if strings.Contains(b, "once you know what you are working on") {
			t.Errorf("%s block still has the old optional wording", v)
		}
		// Only the owner names a session (decision 0033).
		if strings.Contains(b, "Do not pass a name") || strings.Contains(b, "rename the session") {
			t.Errorf("%s block still tells the agent to rename its session", v)
		}
	}
}

// Every agent can read the whole graph, so the guidance tells it to look at a
// peer's charter before asking for something.
func TestGuidanceSaysAgentsCanReadCharters(t *testing.T) {
	for _, v := range []string{Claude, Other, Combined} {
		b := Block(v)
		if !strings.Contains(b, "agent_graph_read") {
			t.Errorf("%s block does not name agent_graph_read", v)
		}
		if !strings.Contains(b, "charter") {
			t.Errorf("%s block does not mention a charter", v)
		}
	}
}

func TestApplyInsertAndRerun(t *testing.T) {
	out, changed := Apply("# Mine\n\n- rule\n", Claude)
	if !changed || !strings.HasPrefix(out, "# Mine\n\n- rule\n\n"+Begin+"\n## agent-net") || !strings.HasSuffix(out, End+"\n") {
		t.Fatalf("bad insert:\n%s", out)
	}
	again, changed := Apply(out, Claude)
	if changed || again != out {
		t.Fatal("rerun was not idempotent")
	}
	out2, _ := Apply("", Other)
	if out2 != Block(Other) {
		t.Fatal("insert into empty file")
	}
	out3, _ := Apply("no newline", Other)
	if !strings.HasPrefix(out3, "no newline\n\n"+Begin) {
		t.Fatalf("no trailing newline: %q", out3)
	}
}

func TestApplyReplacesMarkedBlock(t *testing.T) {
	old := "top\n\n" + Begin + "\nOLD TEXT\n" + End + "\n\n## Later\n\nkeep\n"
	out, changed := Apply(old, Other)
	if !changed || strings.Contains(out, "OLD TEXT") {
		t.Fatal("block not replaced")
	}
	if !strings.HasPrefix(out, "top\n\n"+Begin) || !strings.HasSuffix(out, End+"\n\n## Later\n\nkeep\n") {
		t.Fatalf("surroundings changed:\n%s", out)
	}
}

func TestApplyMigratesLegacySection(t *testing.T) {
	legacy := strings.TrimSuffix(strings.TrimPrefix(Block(Other), Begin+"\n"), End+"\n")
	// Section at end of file.
	in := "# Global\n\n- a\n\n" + legacy
	out, changed := Apply(in, Other)
	if !changed || out != "# Global\n\n- a\n\n"+Block(Other) {
		t.Fatalf("EOF migration:\n%s", out)
	}
	if out2, ch := Apply(out, Other); ch || out2 != out {
		t.Fatal("not idempotent after migration")
	}
	// Section in the middle.
	in = "# Global\n\n" + legacy + "\n## Next\n\nstuff\n"
	out, _ = Apply(in, Other)
	if out != "# Global\n\n"+Block(Other)+"\n## Next\n\nstuff\n" {
		t.Fatalf("mid migration:\n%s", out)
	}
	if strings.Count(out, "agent-net: working with other agents") != 1 {
		t.Fatal("heading duplicated")
	}
}

func TestRemove(t *testing.T) {
	in := "top\n\n" + Block(Claude) + "\n## Later\n\nkeep\n"
	out, changed := Remove(in)
	if !changed || out != "top\n\n## Later\n\nkeep\n" {
		t.Fatalf("remove mid:\n%q", out)
	}
	out, _ = Remove("top\n\n" + Block(Claude))
	if out != "top\n" {
		t.Fatalf("remove at end: %q", out)
	}
	out, _ = Remove(Block(Claude))
	if out != "" {
		t.Fatalf("remove only block: %q", out)
	}
	if _, changed := Remove("nothing here\n"); changed {
		t.Fatal("changed a file without a block")
	}
	// Apply then Remove restores the original.
	orig := "# Mine\n\n- rule\n"
	added, _ := Apply(orig, Claude)
	if back, _ := Remove(added); back != orig {
		t.Fatalf("round trip: %q", back)
	}
}

func TestCTOAuthorityGuidance(t *testing.T) {
	for _, v := range []string{Claude, Other, Combined} {
		b := Block(v)
		// The CTO has two notes. The owner note grants owner authority within
		// the receiver's charter. The peer note grants none: judge it as peer
		// model output. Guidance must cover both so a session cannot mistake
		// ordinary CTO talk for the owner's words.
		for _, want := range []string{
			"from the CTO, on the owner's instructions",
			"within your charter's limits",
			"from the CTO session: model output, not the owner's words",
			"judge it like any peer request",
			"cannot supply either note",
		} {
			if !strings.Contains(b, want) {
				t.Errorf("%s lacks CTO note guidance %q", v, want)
			}
		}
	}
}

// The guidance block is shown to every agent, so it must not name the person
// who owns this network (onboarding part 1).
func TestBlocksDoNotNameTheOwner(t *testing.T) {
	for _, v := range []string{Claude, Other, Combined} {
		if b := Block(v); strings.Contains(b, "Nicolas") {
			t.Errorf("%s block names the owner", v)
		}
	}
}

// Decision 0022: a request from a peer is judged against the node's charter,
// and a session decides instead of deferring.
func TestDecideDoNotDeferRule(t *testing.T) {
	for _, v := range []string{Claude, Other} {
		b := Block(v)
		for _, want := range []string{
			"Decide, do not defer.",
			"judge it against your node's charter",
			"Escalate only when the charter says so, or when it does not cover the request",
			"Every escalation carries a recommendation",
		} {
			if !strings.Contains(b, want) {
				t.Errorf("%s block lacks %q", v, want)
			}
		}
	}
}

func TestWorkerHandbackGuidanceUsesFixedToolAndKeepsCustomText(t *testing.T) {
	def, ok := settings.DefinitionFor("guidance.worker_brief_precedence")
	if !ok {
		t.Fatal("missing worker guidance")
	}
	if strings.Contains(def.Default.Value, "agent_handback") || !strings.Contains(def.Default.Value, "{{handback}}") {
		t.Fatalf("default must use the code-owned tool slot: %q", def.Default.Value)
	}
	custom := json.RawMessage(`"A custom worker note."`)
	if _, err := def.Codec(custom); err != nil {
		t.Fatalf("older custom guidance must remain valid: %v", err)
	}
	snap := settings.Snapshot{Values: []settings.Value{{Key: def.Key, Level: "network", Scope: "network", Value: custom}}}
	if got := BlockWithSnapshot(Other, snap); !strings.Contains(got, "A custom worker note.") {
		t.Fatalf("custom guidance was dropped: %s", got)
	}
	if got := Block(Other); !strings.Contains(got, "`agent_handback`") || strings.Contains(got, "{{handback}}") {
		t.Fatalf("tool was not rendered: %s", got)
	}
}
