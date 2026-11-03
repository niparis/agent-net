package daemon

import (
	"github.com/niparis/agent-net/internal/protocol"
	"testing"
)

func TestHarnessSignInKeepsPrevious(t *testing.T) {
	prev := []protocol.HarnessInfo{{Name: "codex", Path: "fake", SignedIn: "yes", SignIn: "ChatGPT"}}
	got := keepMissingHarnesses(prev, []protocol.HarnessInfo{{Name: "codex", Path: "fake"}}, nil)
	if got[0].SignedIn != "yes" || got[0].SignIn != "ChatGPT" {
		t.Fatal(got)
	}
}
