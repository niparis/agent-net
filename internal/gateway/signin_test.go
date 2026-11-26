package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestWorkerSignInCandidates(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	profiles := []protocol.WorkerProfile{{Name: "test", Harness: "codex", Model: "test-model"}}
	for _, state := range []string{"no", "yes", ""} {
		n := &protocol.NodeView{NodeID: "test-node", Hostname: "test-host", Harnesses: []protocol.HarnessInfo{{Name: "codex", SignedIn: state, Models: []string{"test-model"}}}}
		candidates, notes, _, err := g.workerModelCandidates(n, profiles, protocol.WorkerProviders{}, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if state == "no" {
			if len(candidates) != 0 || !strings.Contains(strings.Join(notes, "; "), "codex skipped on test-host: not signed in") {
				t.Fatalf("%v %v", candidates, notes)
			}
		} else if len(candidates) == 0 {
			t.Fatalf("state %q: no candidates", state)
		}
	}
}

// An agent may name a provider, never a command. Only a provider in the fixed
// allowlist is accepted.
func TestSignInRefusesAnyCommandAndUnknownProvider(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("research", "linux")
	g := &Gateway{store: s}
	raw, _ := json.Marshal(protocol.SignInRaise{Provider: "alibaba", Command: []string{"sh", "-c", "curl attacker"}})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "sign_in.raise", raw); err == nil || !strings.Contains(err.Error(), "never a command") {
		t.Fatalf("command accepted: %v", err)
	}
	open, _ := json.Marshal(protocol.AskOpen{Kind: protocol.AskKindSignIn, Ref: "bl auth login --console", From: "agent-net", Question: "sign in"})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "ask.open", open); err == nil || !strings.Contains(err.Error(), "unknown sign-in provider") {
		t.Fatalf("a command as a ref was accepted: %v", err)
	}
	if _, err := s.RaiseSignIn(node, "bl auth login --console", ""); err == nil || !strings.Contains(err.Error(), "unknown sign-in provider") {
		t.Fatalf("unknown provider accepted: %v", err)
	}
	raw, _ = json.Marshal(protocol.SignInRaise{Provider: "alibaba-bl", Reason: "quota"})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "sign_in.raise", raw); err != nil {
		t.Fatalf("quota provider suffix refused: %v", err)
	}
	asks, _ := s.SignInAsks()
	if len(asks) != 1 || asks[0].Ref != protocol.AskKindSignIn+":"+node+":alibaba" {
		t.Fatalf("provider suffix was not mapped to the allowlist key: %+v", asks)
	}
}

// The CTO may raise a sign-in ask for a known provider on a named machine, and
// list the sign-in asks. The machine is resolved through the normal node lookup.
func TestCTOSignInRaiseForMachineAndList(t *testing.T) {
	s, _ := newTestStore(t)
	mac, _, _ := s.CreateNode("Nicolas-MBP", "darwin")
	g := &Gateway{store: s}
	raw, _ := json.Marshal(protocol.SignInRaise{Provider: "alibaba", Machine: "Nicolas-MBP", Reason: "quota"})
	if _, err := g.handle(context.Background(), &nodeConn{id: "n_other"}, "sign_in.raise", raw); err != nil {
		t.Fatal(err)
	}
	out, err := g.handle(context.Background(), &nodeConn{id: "n_other"}, "sign_in.list", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, ok := out.(protocol.AsksResult)
	if !ok || len(res.Asks) != 1 || res.Asks[0].Node != mac || res.Asks[0].Question != "Sign in to Alibaba Bailian CLI on Nicolas-MBP" {
		t.Fatalf("sign-in list: %#v", out)
	}
}
