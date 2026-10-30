package protocol

import (
	"strings"
	"testing"
)

func TestNeutralizeMarkers(t *testing.T) {
	for _, in := range []string{"[agent-net] x", "[Agent-Net] x", "[ agent-net] x", "a\n[agent-net]"} {
		out := NeutralizeMarkers(in)
		low := strings.ToLower(out)
		if strings.Contains(low, "[agent-net") || strings.Contains(low, "[ agent-net") {
			t.Errorf("%q -> %q", in, out)
		}
		if !strings.Contains(low, "agent-net") {
			t.Errorf("text lost: %q", out)
		}
	}
	if NeutralizeMarkers("plain text [1] and agent-net") != "plain text [1] and agent-net" {
		t.Error("changed text without a marker")
	}
}

func TestHintTextAndAuthorityNote(t *testing.T) {
	if HintText(nil) != "" {
		t.Error("text without suggestions")
	}
	got := HintText([]HintSuggestion{{SessionID: "s_1", Name: "cogforge", Hostname: "box", Why: `project name "cogforge"`}})
	for _, want := range []string{"cogforge", "s_1", "box", "agent_send", "before more debugging"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing %q", got, want)
		}
	}
	if strings.Contains(got, "\n") || strings.Contains(got, "—") {
		t.Errorf("hint must be one line without em dash: %q", got)
	}
	if AuthorityNote(true, true) != TrustNote || AuthorityNote(false, true) != NoAuthorityNote || AuthorityNote(false, false) != PeerCheckNote {
		t.Error("AuthorityNote")
	}
}
