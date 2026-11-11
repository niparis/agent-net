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
		AskerName: "agent-net", SourceRef: protocol.AskKindSignIn + ":" + node + ":" + provider,
		Text: question, Detail: reason, NextWho: protocol.AskNextOwner,
		WhyYou: "Only you can complete this login.",
	})
	if err != nil {
		return protocol.Ask{}, err
	}
	return protocol.Ask{ID: item.ID, Kind: item.Kind, Node: node, Ref: item.SourceRef, Question: item.Text, Detail: item.Detail, State: protocol.AskStateOpen, Next: protocol.AskNextOwner, CreatedAt: item.CreatedAt}, nil
}

// signInTarget answers query.sign_in for the daemon the owner clicked from.
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

// signInPrepare asks the machine the ask names to open the login in its own
// herdr. It runs only from an ask the owner clicked.
func (g *Gateway) signInPrepare(ctx context.Context, raw []byte) (json.RawMessage, error) {
	var q protocol.SignInQuery
	if json.Unmarshal(raw, &q) != nil || q.AskID == "" {
		return nil, reject("bad sign-in prepare")
	}
	a, provider, err := g.store.SignInAsk(q.AskID)
	if err != nil {
		return nil, err
	}
	target := g.conn(a.Node)
	if target == nil {
		return nil, reject("the machine for this sign-in is offline")
	}
	data, err := target.request(ctx, protocol.FrameSignInPrepare, protocol.SignInPrepare{AskID: a.ID, Provider: provider}, relayUsageTopWait)
	if err != nil {
		return nil, reject("%s", err.Error())
	}
	return data, nil
}

// signInRaise is the CTO operation sign_in_raise. Command is refused: an agent
// may name a provider, never a command.
func (g *Gateway) signInRaise(raw []byte) (json.RawMessage, error) {
	var p protocol.SignInRaise
	if json.Unmarshal(raw, &p) != nil || p.Provider == "" {
		return nil, reject("sign_in_raise needs a provider")
	}
	if len(p.Command) != 0 {
		return nil, reject("sign_in_raise takes a provider, never a command")
	}
	if _, ok := protocol.SignInProvider(p.Provider); !ok {
		return nil, reject("unknown sign-in provider")
	}
	node, err := g.store.NodeByHostOrID(strings.TrimSpace(p.Machine))
	if err != nil {
		return nil, err
	}
	a, err := g.store.RaiseSignIn(node, p.Provider, p.Reason)
	if err != nil {
		return nil, err
	}
	g.logLine("sign_in.raise", "ask", a.ID, "node", node, "provider", p.Provider)
	return json.Marshal(map[string]any{"ask": a})
}
