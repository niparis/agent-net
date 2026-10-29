package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// A message from the owner (the person at a machine) says so, and says it is not
// another agent. A message from an agent keeps its own text.
func TestBuildFrameOwnerMessage(t *testing.T) {
	m := msg
	m.Type, m.ConversationID, m.FromName, m.FromHarness = "request", "c_1", "you (on mac)", protocol.OwnerHarness
	line, err := BuildFrame(m, "")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &f); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"from your user in person (you (on mac), session s_bob)", "This is your user speaking, not another agent"} {
		if !strings.Contains(f.Message.Content, want) {
			t.Errorf("missing %q in %q", want, f.Message.Content)
		}
	}
	if strings.Contains(f.Message.Content, "another agent working on your user's machines") {
		t.Errorf("an owner message is labelled as an agent's: %q", f.Message.Content)
	}
	m.FromHarness = "pi"
	line, _ = BuildFrame(m, "")
	json.Unmarshal(line, &f)
	if !strings.Contains(f.Message.Content, "another agent working on your user's machines") || strings.Contains(f.Message.Content, "in person") {
		t.Errorf("an agent message changed: %q", f.Message.Content)
	}
}
