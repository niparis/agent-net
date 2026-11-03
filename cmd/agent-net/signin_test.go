package main

import (
	"bytes"
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestHarnessSignInRendering(t *testing.T) {
	var b bytes.Buffer
	printHarnessDetails(&b, []protocol.NodeView{{Hostname: "fixture", Harnesses: []protocol.HarnessInfo{{Name: "claude", Path: "/fixture/claude", SignedIn: "yes"}, {Name: "codex", Path: "/fixture/codex", SignedIn: "no"}}}})
	if !strings.Contains(b.String(), "signed in") || !strings.Contains(b.String(), "not signed in") {
		t.Fatal(b.String())
	}
	t.Log("\n" + b.String())
}
