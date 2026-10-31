package setup

import (
	"bytes"
	"strings"
	"testing"
)

func TestProtectedProviderProposal(t *testing.T) {
	var out bytes.Buffer
	proposeProtectedProvider(&out)
	for _, want := range []string{"anthropic", "orchestrator", "subscription", "agent-net workers protect anthropic"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("proposal missing %q: %s", want, out.String())
		}
	}
}
