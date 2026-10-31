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
