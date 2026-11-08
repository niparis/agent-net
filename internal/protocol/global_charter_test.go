package protocol

import (
	"strings"
	"testing"
)

// A session that no node covers gets the owner's global level, in the same
// line shape as a node's effective charter.
func TestGlobalCharterTextForASessionWithoutANode(t *testing.T) {
	g := GlobalCharter{Rules: "plain English", Refuses: "secrets", Escalate: "ask the owner"}
	got := GlobalCharterText(g)
	for _, want := range []string{"rules: plain English", "refuses: secrets", "escalate: ask the owner", "[agent-net]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if GlobalCharterText(GlobalCharter{}) != "" {
		t.Fatal("no global level must give no text")
	}
}

func TestInstructionsWithCharterFitsTheClientCap(t *testing.T) {
	short := InstructionsWith("[agent-net] charter\nrules: x", "base instructions")
	if !strings.HasPrefix(short, "base instructions\n\n") || !strings.HasSuffix(short, "rules: x") {
		t.Fatalf("short: %q", short)
	}
	if got := InstructionsWith("", "base"); got != "base" {
		t.Fatalf("no charter must keep the fixed text: %q", got)
	}
	long := InstructionsWith(strings.Repeat("rule line number one\n", 300), "base instructions")
	if n := len([]rune(long)); n > MaxInstructionsRunes {
		t.Fatalf("%d runes, cap %d", n, MaxInstructionsRunes)
	}
	if !strings.HasPrefix(long, "base instructions\n\n") || !strings.Contains(long, "(cut here") {
		t.Fatalf("long: %q", long[:80])
	}
}
