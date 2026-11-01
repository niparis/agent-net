package gateway

import (
	"context"
	"strings"
	"testing"
)

func TestSkillTextRequiresOwnerAtGateway(t *testing.T) {
	g, node := scopeGateway(t)
	for _, raw := range []string{`{"item":"x","path":"SKILL.md"}`, `{"item":"x","path":"SKILL.md","owner":"s_fake"}`} {
		if _, err := g.handle(context.Background(), &nodeConn{id: node}, "tools.read_file", []byte(raw)); err == nil || !strings.Contains(err.Error(), "owner authority") {
			t.Fatalf("gateway accepted ownerless read: %v", err)
		}
	}
}

func TestSkillFolderAndCopyChoiceRequireOwner(t *testing.T) {
	g, node := scopeGateway(t)
	for _, method := range []string{"tools.import_folder", "tools.keep_copy"} {
		if _, err := g.handle(context.Background(), &nodeConn{id: node}, method, []byte(`{"owner":"s_fake"}`)); err == nil || !strings.Contains(err.Error(), "owner authority") {
			t.Fatalf("%s: %v", method, err)
		}
	}
}
