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
		candidates, notes, _, err := g.workerModelCandidates(n, profiles, protocol.WorkerProviders{}, nil, "", false, protocol.ProtectedProviders{})
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

// The daemon raises one sign_in ask per machine and provider. A repeat
// refreshes that one ask, and a close with "signed in" takes it off the Desk.
func TestSignInAskIsOnePerMachineAndProvider(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, _ := s.CreateNode("research", "linux")
	g := &Gateway{store: s}
	for i := 0; i < 3; i++ {
		raw, _ := json.Marshal(protocol.SignInRaise{Provider: "alibaba", Reason: "the Alibaba token-plan quota needs your console login"})
		if _, err := g.handle(context.Background(), &nodeConn{id: node}, "sign_in.raise", raw); err != nil {
			t.Fatal(err)
		}
	}
	asks, err := s.SignInAsks()
	if err != nil {
		t.Fatal(err)
	}
	if len(asks) != 1 {
		t.Fatalf("want one sign-in ask, got %+v", asks)
	}
	a := asks[0]
	if a.Kind != protocol.AskKindSignIn || a.Node != node || a.FromKind != protocol.AskFromGateway ||
		a.Ref != protocol.AskKindSignIn+":"+node+":alibaba" || a.Detail == "" || a.Next != protocol.AskNextOwner {
		t.Fatalf("sign-in ask shape: %+v", a)
	}
	closeRaw, _ := json.Marshal(protocol.SignInClose{Provider: "alibaba", Reason: protocol.AskReasonSignedIn})
	if _, err := g.handle(context.Background(), &nodeConn{id: node}, "sign_in.close", closeRaw); err != nil {
		t.Fatal(err)
	}
	if asks, _ := s.SignInAsks(); len(asks) != 0 {
		t.Fatalf("closed sign-in ask stays: %+v", asks)
	}
	status, answer, _ := askRow(t, s, `id=?`, a.ID)
	if status != "closed" || answer != protocol.AskReasonSignedIn {
		t.Fatalf("closed ask: %s %s", status, answer)
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

// The prepare frame opens a login pane, so it needs the owner's authority
// before any frame goes to the target machine. A read of provider and host
// needs no owner and opens nothing.
func TestSignInPrepareNeedsOwnerBeforeTargetFrame(t *testing.T) {
	s, _ := newTestStore(t)
	requester, _, _ := s.CreateNode("research", "linux")
	target, _, _ := s.CreateNode("mac", "darwin")
	ask, err := s.RaiseSignIn(target, "alibaba", "")
	if err != nil {
		t.Fatal(err)
	}
	targetConn := &nodeConn{id: target}
	g := &Gateway{store: s, conns: map[string]*nodeConn{target: targetConn}}
	raw, _ := json.Marshal(protocol.SignInQuery{AskID: ask.ID, Owner: "s_not_the_owner"})
	if _, err := g.signInPrepare(context.Background(), &nodeConn{id: requester}, raw); err == nil || !strings.Contains(err.Error(), "owner authority required") {
		t.Fatalf("prepare without an owner: %v", err)
	}
	targetConn.pmu.Lock()
	sent := targetConn.nextID
	targetConn.pmu.Unlock()
	if sent != 0 {
		t.Fatalf("a refused prepare sent %d frame(s) to the target", sent)
	}
	ownerID, err := s.UpsertSession(requester, owner("research"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(protocol.SignInQuery{AskID: ask.ID, Owner: ownerID})
	_, _ = g.signInPrepare(context.Background(), &nodeConn{id: requester}, raw)
	targetConn.pmu.Lock()
	sent = targetConn.nextID
	targetConn.pmu.Unlock()
	if sent != 1 {
		t.Fatalf("the owner's prepare did not reach the target: %d frame(s)", sent)
	}
}
