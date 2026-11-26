package gateway

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Sign-in asks name a machine, a CLI label and the reason. The command comes
// from the fixed allowlist in protocol/signin.go, keyed by provider. No
// caller supplies an argv, and an unknown provider is refused.

// SignInAsk returns one open sign_in ask and the provider it names.
func (s *Store) SignInAsk(id string) (protocol.Ask, string, error) {
	var a protocol.Ask
	var status, ref string
	err := s.db.QueryRow(`SELECT id,kind,node,source_ref,text,status FROM inbox_items WHERE id=?`, id).
		Scan(&a.ID, &a.Kind, &a.Node, &ref, &a.Question, &status)
	if err != nil {
		return a, "", err
	}
	if a.Kind != protocol.AskKindSignIn {
		return a, "", reject("ask %s is not a sign-in ask", id)
	}
	if status != "open" {
		return a, "", reject("sign-in ask is not open")
	}
	provider := strings.TrimPrefix(ref, protocol.AskKindSignIn+":"+a.Node+":")
	if provider == ref {
		return a, "", reject("sign-in ask has no provider")
	}
	if _, ok := protocol.SignInProvider(provider); !ok {
		return a, "", reject("unknown sign-in provider")
	}
	return a, provider, nil
}

// SignInAsks lists the open sign-in asks.
func (s *Store) SignInAsks() ([]protocol.Ask, error) {
	asks, err := s.ListAsks()
	if err != nil {
		return nil, err
	}
	out := []protocol.Ask{}
	for _, a := range asks {
		if a.Kind == protocol.AskKindSignIn {
			out = append(out, a)
		}
	}
	return out, nil
}

// RaiseSignIn asks the owner to sign in to a known CLI on one machine. It
// grants nothing: only the owner's click opens a pane.
func (s *Store) RaiseSignIn(node, provider, reason string) (protocol.Ask, error) {
	info, ok := protocol.SignInProvider(provider)
	if !ok {
		return protocol.Ask{}, reject("unknown sign-in provider")
	}
	host := s.hostname(node)
	if host == "" {
		host = node
	}
	if strings.TrimSpace(reason) == "" {
		reason = info.Reason
	}
	question := "Sign in to " + info.Label + " on " + host
	item, err := s.createInboxItem(s.db, protocol.InboxItem{
		Kind: protocol.AskKindSignIn, SessionID: node, Node: node, FromKind: protocol.AskFromGateway,
		AskerName: "agent-net", SourceRef: protocol.AskKindSignIn + ":" + node + ":" + info.Provider,
		Text: question, Detail: reason, NextWho: protocol.AskNextOwner,
		WhyYou: "Only you can complete this login.",
	})
	if err != nil {
		return protocol.Ask{}, err
	}
	return protocol.Ask{ID: item.ID, Kind: item.Kind, Node: node, Ref: item.SourceRef, Question: item.Text, Detail: item.Detail, State: protocol.AskStateOpen, Next: protocol.AskNextOwner, CreatedAt: item.CreatedAt}, nil
}

// CloseSignIn closes the open sign-in ask of one provider on one machine.
func (s *Store) CloseSignIn(node, provider, reason string) error {
	key, ok := protocol.SignInProviderKey(provider)
	if !ok {
		return reject("unknown sign-in provider")
	}
	info, _ := protocol.SignInProvider(key)
	if reason == "" {
		reason = protocol.AskReasonSignedIn
	}
	return s.closeAskRef(s.db, protocol.AskKindSignIn+":"+node+":"+info.Provider, reason)
}

// signInTarget answers query.sign_in for the daemon the owner clicked from.
// This is a read of the provider and host only; it opens no pane.
func (g *Gateway) signInTarget(raw []byte) (json.RawMessage, error) {
	var q protocol.SignInQuery
	if json.Unmarshal(raw, &q) != nil || q.AskID == "" {
		return nil, reject("bad sign-in query")
	}
	a, provider, err := g.store.SignInAsk(q.AskID)
	if err != nil {
		return nil, err
	}
	info, _ := protocol.SignInProvider(provider)
	return json.Marshal(protocol.SignInTarget{
		AskID: a.ID, Provider: provider, Label: info.Label,
		NodeID: a.Node, Hostname: g.store.hostname(a.Node),
	})
}

// signInPrepare asks a named machine to open the login in its own herdr. It
// checks owner authority before any frame goes to the target machine, and the
// provider must be on the allowlist. No stored ask is needed.
func (g *Gateway) signInPrepare(ctx context.Context, nc *nodeConn, raw []byte) (json.RawMessage, error) {
	var q protocol.SignInPrepareRequest
	if json.Unmarshal(raw, &q) != nil || strings.TrimSpace(q.Machine) == "" || strings.TrimSpace(q.Provider) == "" {
		return nil, reject("bad sign-in prepare")
	}
	if !g.validToolOwner(nc, q.Owner) {
		return nil, reject("owner authority required")
	}
	return g.openSignInOnMachine(ctx, q.Machine, q.Provider)
}

// openSignInOnMachine resolves a machine and allowlisted provider and asks that
// machine's daemon to open the login in its own herdr. A caller never supplies
// a command: the daemon reads it from the fixed table.
func (g *Gateway) openSignInOnMachine(ctx context.Context, machine, provider string) (json.RawMessage, error) {
	info, ok := protocol.SignInProvider(provider)
	if !ok {
		return nil, reject("unknown sign-in provider")
	}
	node, err := g.store.NodeByHostOrID(strings.TrimSpace(machine))
	if err != nil {
		return nil, err
	}
	target := g.conn(node)
	if target == nil {
		return nil, reject("the machine for this sign-in is offline")
	}
	data, err := target.request(ctx, protocol.FrameSignInPrepare, protocol.SignInPrepare{Provider: info.Provider}, relayUsageTopWait)
	if err != nil {
		return nil, reject("%s", err.Error())
	}
	return data, nil
}

// signInOpen is FrameSignInOpen: the CTO opens a login on a named machine on
// the owner's quoted words, through the owner operation sign_in_open. The
// daemon verifies the words and names the owner session; the gateway refuses
// anything without that proof.
func (g *Gateway) signInOpen(ctx context.Context, nc *nodeConn, raw []byte) (json.RawMessage, error) {
	var p protocol.SignInOpenFrame
	if json.Unmarshal(raw, &p) != nil {
		return nil, reject("bad sign-in open")
	}
	if len(p.Request.Command) != 0 {
		return nil, reject("sign_in_open takes a provider, never a command")
	}
	if !g.validToolOwner(nc, p.Owner) {
		return nil, reject("owner authority required")
	}
	if strings.TrimSpace(p.Request.OwnerWords) == "" || !p.WordsVerified {
		return nil, reject("owner_words were not verified: the owner must have typed them")
	}
	if strings.TrimSpace(p.Request.Machine) == "" || strings.TrimSpace(p.Request.Provider) == "" {
		return nil, reject("sign_in_open needs a machine and a provider")
	}
	return g.openSignInOnMachine(ctx, p.Request.Machine, p.Request.Provider)
}

// signInRaise raises one sign-in ask for a known provider on a machine. The
// caller may name a provider, never a command. An empty machine means the node
// that sent the frame: the daemon raises for its own quota error.
func (g *Gateway) signInRaise(nc *nodeConn, raw []byte) (json.RawMessage, error) {
	var p protocol.SignInRaise
	if json.Unmarshal(raw, &p) != nil || p.Provider == "" {
		return nil, reject("sign_in_raise needs a provider")
	}
	if len(p.Command) != 0 {
		return nil, reject("sign_in_raise takes a provider, never a command")
	}
	key, ok := protocol.SignInProviderKey(p.Provider)
	if !ok {
		return nil, reject("unknown sign-in provider")
	}
	info, _ := protocol.SignInProvider(key)
	node := nc.id
	if strings.TrimSpace(p.Machine) != "" {
		var err error
		node, err = g.store.NodeByHostOrID(strings.TrimSpace(p.Machine))
		if err != nil {
			return nil, err
		}
	}
	a, err := g.store.RaiseSignIn(node, info.Provider, p.Reason)
	if err != nil {
		return nil, err
	}
	g.logLine("sign_in.raise", "ask", a.ID, "node", node, "provider", info.Provider)
	return json.Marshal(map[string]any{"ask": a})
}

// signInClose closes the sign-in ask of one provider on the node that sent the
// frame, once its login remedy is gone.
func (g *Gateway) signInClose(nc *nodeConn, raw []byte) error {
	var p protocol.SignInClose
	if json.Unmarshal(raw, &p) != nil || p.Provider == "" {
		return reject("sign_in_close needs a provider")
	}
	if _, ok := protocol.SignInProvider(p.Provider); !ok {
		return reject("unknown sign-in provider")
	}
	return g.store.CloseSignIn(nc.id, p.Provider, p.Reason)
}

// signInTransfer relays credentials only to the named machine and allowlisted
// provider. Neither the token nor the other node's output is written to the
// store or log.
func (g *Gateway) signInTransfer(ctx context.Context, nc *nodeConn, raw []byte) (json.RawMessage, error) {
	var p protocol.SignInTransfer
	if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Machine) == "" || p.Provider == "" || p.Token == "" {
		return nil, reject("bad sign-in transfer")
	}
	if !g.validToolOwner(nc, p.Owner) {
		return nil, reject("owner authority required")
	}
	info, ok := protocol.SignInProvider(p.Provider)
	if !ok {
		return nil, reject("unknown sign-in provider")
	}
	node, err := g.store.NodeByHostOrID(strings.TrimSpace(p.Machine))
	if err != nil {
		return nil, err
	}
	target := g.conn(node)
	if target == nil {
		return nil, reject("the machine for this sign-in is offline")
	}
	_, err = target.request(ctx, protocol.FrameSignInImport, protocol.SignInImport{Provider: info.Provider, Token: p.Token}, defaultRelayWait)
	if err != nil {
		return nil, reject("the target could not complete sign-in")
	}
	return json.Marshal(map[string]bool{"ok": true})
}
