package protocol

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Every owner sees the same notes. None of them may name the person who owns
// this network (onboarding part 1).
func TestDeliveredNotesDoNotNameTheOwner(t *testing.T) {
	notes := []struct{ name, text string }{
		{"CTONote", CTONote},
		{"OverruleNote", OverruleNote},
		{"written approval", WrittenApprovalNote("the owner", "merge abc123", time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))},
	}
	for _, n := range notes {
		if strings.Contains(n.text, "Nicolas") {
			t.Errorf("%s names the owner: %q", n.name, n.text)
		}
	}
}

// The Pi and OpenCode plugins carry their own copies of the delivered notes.
func TestPluginNotesDoNotNameTheOwner(t *testing.T) {
	for _, path := range []string{"../../plugins/pi/agent-net.ts", "../../plugins/opencode/agent-net.ts"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "Nicolas") {
			t.Errorf("%s names the owner in a delivered note", path)
		}
	}
}

// Agents never see the owner's chosen CTO title (onboarding part 5). The note
// they match on keeps the fixed word, or a careful agent stops trusting it.
func TestAgentNotesKeepTheFixedCTOName(t *testing.T) {
	if title := CTOTitleOrDefault("Atlas"); title != "Atlas" {
		t.Fatalf("owner title = %q", title)
	}
	if !strings.Contains(CTONote, "the CTO") {
		t.Fatalf("the CTO note lost the fixed word: %q", CTONote)
	}
	if strings.Contains(CTONote, "Atlas") {
		t.Fatalf("the CTO note follows the owner's title: %q", CTONote)
	}
}
