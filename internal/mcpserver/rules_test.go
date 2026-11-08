package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// joinedLiterals removes the `" +\n "` joints of a TS string concatenation, so
// a long sentence written over several lines can be found as one string.
func joinedLiterals(src string) string {
	return regexp.MustCompile("[\"`]\\s*\\+\\s*[\"`]").ReplaceAllString(src, "")
}

// The Pi extension and the OpenCode plugin carry copies of the words the Go
// side writes: the delivery notes and the two rules in the tool descriptions.
// They must not drift apart.
func TestPluginsCarryTheSameNotesAndRules(t *testing.T) {
	for _, path := range []string{"../../plugins/pi/agent-net.ts", "../../plugins/opencode/agent-net.ts"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := joinedLiterals(string(b))
		for name, want := range map[string]string{
			"TrustNotePrefix":        protocol.TrustNotePrefix,
			"TrustNoteSuffix":        protocol.TrustNoteSuffix,
			"MergeFullText":          protocol.MergeFullText,
			"MergeDelegatedText":     protocol.MergeDelegatedText,
			"MergeAskText":           protocol.MergeAskText,
			"OwnerInstructionPrefix": protocol.OwnerInstructionPrefix,
			"OwnerInstructionAt":     protocol.OwnerInstructionAt,
			"OwnerInstructionQuote":  protocol.OwnerInstructionQuote,
			"OwnerInstructionSuffix": protocol.OwnerInstructionSuffix,
			"CTONote":                protocol.CTONote,
			"NoAuthorityNote":        protocol.NoAuthorityNote,
			"PeerCheckNote":          protocol.PeerCheckNote,
			"AskPeerRule":            AskPeerRule,
			"RelayRule":              RelayRule,
			"peer note (tools)":      strings.TrimSpace(peerNote),
		} {
			if !strings.Contains(src, want) {
				t.Errorf("%s does not carry %s:\n%s", path, name, want)
			}
		}
	}
}

func TestToolDescriptionsCarryTheAnnounceRuleAndExpertise(t *testing.T) {
	call := func(context.Context, string, any) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }
	t.Setenv("AGENT_NET_WORKER", "")
	s := New(call, "test")
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cl := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "1"}, nil)
	sess, err := cl.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	desc := map[string]string{}
	var announceSchema string
	for _, tl := range list.Tools {
		desc[tl.Name] = tl.Description
		if tl.Name == "agent_announce" {
			b, _ := json.Marshal(tl.InputSchema)
			announceSchema = string(b)
		}
	}
	if !strings.Contains(desc["agent_announce"], "expertise") || !strings.Contains(announceSchema, `"expertise"`) {
		t.Errorf("agent_announce: %s %s", desc["agent_announce"], announceSchema)
	}
	for name, d := range desc {
		if strings.Contains(d, "\u2014") {
			t.Errorf("%s has an em dash", name)
		}
	}
}
