package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/authority"
	"github.com/niparis/agent-net/internal/mcpserver"
)

// The daemon's three readers of the owner-only list (the class table, the op
// gate and the CTO's instructions) agree with the one list. The gateway refusal
// and the tool-decide answer have their own tests beside the code they test.
func TestCTOOwnerOnlyListIsTheOneList(t *testing.T) {
	needWords := map[string]bool{}
	for tool, actions := range ctoActionClasses {
		for action, class := range actions {
			if class == ctoOwnerOnly {
				needWords[action] = true
			}
			if authority.OwnerOnly(action) && class != ownerGate(action) {
				t.Errorf("%s %s has class %s", tool, action, class)
			}
		}
	}
	for _, name := range authority.OwnerWordsNames() {
		if !needWords[name] {
			t.Errorf("%s is on the list but no action class needs the owner's words", name)
		}
		delete(needWords, name)
		if !strings.Contains(ctoBrief, name) {
			t.Errorf("the CTO's instructions do not name %s", name)
		}
	}
	for name := range needWords {
		t.Errorf("%s needs the owner's words but is not on the list", name)
	}

	for _, op := range mcpserver.CTOOps() {
		name := op.Action
		if op.Method != "" {
			name = op.Method
		}
		gate := ctoOpGate(op)
		switch {
		case authority.Irreversible(name) && gate != "irreversible":
			t.Errorf("op %s gate %s, want irreversible", op.Name, gate)
		case authority.NeedsOwnerWords(name) && gate != "owner_words":
			t.Errorf("op %s gate %s, want owner_words", op.Name, gate)
		case !authority.OwnerOnly(name) && gate != "direct":
			t.Errorf("op %s gate %s, want direct", op.Name, gate)
		}
	}
}
