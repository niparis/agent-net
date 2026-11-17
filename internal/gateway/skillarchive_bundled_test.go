package gateway

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The daemon may send only the skill this binary bundles. Any other name is
// refused before its bytes are read.
func TestSkillArchiveBundledRefusesOtherNames(t *testing.T) {
	g := archiveGateway(t)
	_, err := g.skillArchiveBundled(protocol.SkillArchiveBundled{Name: "some-other-skill", Bundle: []byte("not a bundle")}, "cto")
	if err == nil || !strings.Contains(err.Error(), "agent-net bundles only agent-net-setup") {
		t.Fatalf("a bundled skill with another name was accepted or refused without saying why: %v", err)
	}
}
