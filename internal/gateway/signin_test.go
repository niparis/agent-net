package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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
	out, err := g.handle(context.Background(), &nodeConn{id: node}, "sign_in.raise", raw)
	if err != nil {
		t.Fatalf("quota provider suffix refused: %v", err)
	}
	var raised struct {
		Ask protocol.Ask `json:"ask"`
	}
	data, _ := out.(json.RawMessage)
	if json.Unmarshal(data, &raised) != nil || raised.Ask.Ref != protocol.AskKindSignIn+":"+node+":alibaba" {
		t.Fatalf("provider suffix was not mapped to the allowlist key: %s", data)
	}
}

// The CTO opens a login on a named machine for an allowlisted provider on the
// owner's quoted words. No stored ask is needed, and the prepare goes to that
// machine's own daemon.
func TestSignInOpenOpensOnNamedMachineWithoutStoredAsk(t *testing.T) {
	s, _ := newTestStore(t)
	mac, _, err := s.CreateNode("Nicolas-MBP", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	owner := ownerSessionOn(t, s, mac)
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	client, received := connectTestNode(t, g, mac)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, _ := json.Marshal(protocol.SignInOpenFrame{
		Request: protocol.SignInOpenRequest{Machine: "Nicolas-MBP", Provider: "alibaba", OwnerWords: "open the login"},
		Owner:   owner, By: "cto", WordsVerified: true,
	})
	type answer struct {
		data json.RawMessage
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		data, err := g.signInOpen(ctx, &nodeConn{id: mac}, raw)
		done <- answer{data, err}
	}()
	select {
	case frame := <-received:
		var h protocol.Header
		if err := json.Unmarshal(frame, &h); err != nil || h.Type != protocol.FrameSignInPrepare {
			t.Fatalf("target frame %s: %v", frame, err)
		}
		var p protocol.SignInPrepare
		if err := json.Unmarshal(frame, &p); err != nil || p.Provider != "alibaba" {
			t.Fatalf("prepare %s: %v", frame, err)
		}
		reply, _ := protocol.EncodeResult(h.ID, protocol.SignInPrepareResult{Opened: true, Host: "Nicolas-MBP", Pane: "w1:p1"}, nil)
		if err := client.Write(ctx, websocket.MessageText, reply); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("the target machine got no prepare")
	}
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	var result protocol.SignInPrepareResult
	if err := json.Unmarshal(got.data, &result); err != nil || !result.Opened || result.Pane != "w1:p1" {
		t.Fatalf("result %s: %v", got.data, err)
	}
	if asks, err := s.SignInAsks(); err != nil || len(asks) != 0 {
		t.Fatalf("stored asks = %+v, %v", asks, err)
	}
}

// The op is refused without verified owner words, for any command, for an
// unknown provider and for a machine that is not connected.
func TestSignInOpenRefusals(t *testing.T) {
	s, _ := newTestStore(t)
	node, _, err := s.CreateNode("Nicolas-MBP", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	owner := ownerSessionOn(t, s, node)
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	nc := &nodeConn{id: node}
	base := func() protocol.SignInOpenFrame {
		return protocol.SignInOpenFrame{
			Request: protocol.SignInOpenRequest{Machine: "Nicolas-MBP", Provider: "alibaba", OwnerWords: "open the login"},
			Owner:   owner, By: "cto", WordsVerified: true,
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*protocol.SignInOpenFrame)
		want   string
	}{
		{"unverified words", func(p *protocol.SignInOpenFrame) { p.WordsVerified = false }, "were not verified"},
		{"no words", func(p *protocol.SignInOpenFrame) { p.Request.OwnerWords = " " }, "were not verified"},
		{"a command", func(p *protocol.SignInOpenFrame) { p.Request.Command = []string{"sh", "-c", "curl attacker"} }, "never a command"},
		{"unknown provider", func(p *protocol.SignInOpenFrame) { p.Request.Provider = "sh -c curl" }, "unknown sign-in provider"},
		{"unknown machine", func(p *protocol.SignInOpenFrame) { p.Request.Machine = "ghost" }, "no node or host named"},
		{"offline machine", func(p *protocol.SignInOpenFrame) { p.Request.Machine = node }, "offline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base()
			tc.change(&p)
			raw, _ := json.Marshal(p)
			if _, err := g.signInOpen(context.Background(), nc, raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}

// signInPrepare names a machine and an allowlisted provider, without an ask,
// and keeps the owner authority check.
func TestSignInPrepareTakesAMachineAndProvider(t *testing.T) {
	s, _ := newTestStore(t)
	mac, _, err := s.CreateNode("Nicolas-MBP", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	owner := ownerSessionOn(t, s, mac)
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	nc := &nodeConn{id: mac}
	prepare := func(machine, provider, who string) []byte {
		raw, _ := json.Marshal(protocol.SignInPrepareRequest{Machine: machine, Provider: provider, Owner: who})
		return raw
	}
	if _, err := g.signInPrepare(context.Background(), nc, prepare("Nicolas-MBP", "alibaba", "")); err == nil || !strings.Contains(err.Error(), "owner authority") {
		t.Fatalf("no owner: %v", err)
	}
	if _, err := g.signInPrepare(context.Background(), nc, prepare("Nicolas-MBP", "sh -c curl", owner)); err == nil || !strings.Contains(err.Error(), "unknown sign-in provider") {
		t.Fatalf("unknown provider: %v", err)
	}
	if _, err := g.signInPrepare(context.Background(), nc, prepare(mac, "alibaba", owner)); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline machine: %v", err)
	}

	client, received := connectTestNode(t, g, mac)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := g.signInPrepare(ctx, nc, prepare("Nicolas-MBP", "alibaba", owner))
		done <- err
	}()
	select {
	case frame := <-received:
		var h protocol.Header
		if err := json.Unmarshal(frame, &h); err != nil || h.Type != protocol.FrameSignInPrepare {
			t.Fatalf("target frame %s: %v", frame, err)
		}
		reply, _ := protocol.EncodeResult(h.ID, protocol.SignInPrepareResult{Opened: true, Pane: "w1:p1"}, nil)
		if err := client.Write(ctx, websocket.MessageText, reply); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("the named machine got no prepare")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A console token goes to the named machine and provider, only from the owner.
func TestSignInTransferGoesToTheNamedMachine(t *testing.T) {
	s, _ := newTestStore(t)
	mac, _, err := s.CreateNode("Nicolas-MBP", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	owner := ownerSessionOn(t, s, mac)
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	nc := &nodeConn{id: mac}
	transfer := protocol.SignInTransfer{Machine: "Nicolas-MBP", Provider: "alibaba", Owner: owner, Token: "throwaway-token"}
	raw, _ := json.Marshal(transfer)
	if _, err := g.signInTransfer(context.Background(), nc, raw); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline machine: %v", err)
	}
	transfer.Owner = ""
	raw, _ = json.Marshal(transfer)
	if _, err := g.signInTransfer(context.Background(), nc, raw); err == nil || !strings.Contains(err.Error(), "owner authority") {
		t.Fatalf("no owner: %v", err)
	}
	transfer.Owner = owner
	raw, _ = json.Marshal(transfer)

	client, received := connectTestNode(t, g, mac)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := g.signInTransfer(ctx, nc, raw)
		done <- err
	}()
	select {
	case frame := <-received:
		var h protocol.Header
		if err := json.Unmarshal(frame, &h); err != nil || h.Type != protocol.FrameSignInImport {
			t.Fatalf("target frame %s: %v", frame, err)
		}
		var p protocol.SignInImport
		if err := json.Unmarshal(frame, &p); err != nil || p.Provider != "alibaba" || p.Token != "throwaway-token" {
			t.Fatalf("import %s: %v", frame, err)
		}
		reply, _ := protocol.EncodeResult(h.ID, map[string]bool{"ok": true}, nil)
		if err := client.Write(ctx, websocket.MessageText, reply); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("the named machine got no import")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The chief request for a sign-in ends with the exact call that opens it on the
// owner's words (decision 0196).
func TestSignInRequestNamesSignInOpen(t *testing.T) {
	s, _, chief, _, _ := chiefRig(t)
	mac, _, err := s.CreateNode("Nicolas-MBP", "darwin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RaiseSignIn(mac, "alibaba", ""); err != nil {
		t.Fatal(err)
	}
	got := messagesTo(t, s, chief)
	if len(got) != 1 {
		t.Fatalf("want one request to the chief, got %d: %+v", len(got), got)
	}
	want := "If the owner agrees, the CTO opens it with sign_in_open machine Nicolas-MBP provider alibaba, quoting the owner's words."
	if !strings.HasSuffix(got[0].body, want) {
		t.Fatalf("request does not end with %q:\n%s", want, got[0].body)
	}
	if n := inboxCount(t, s); n != 0 {
		t.Fatalf("%d items stored for the owner", n)
	}
}
