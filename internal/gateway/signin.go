package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Sign-in requests name a machine, a CLI label and the reason. The command
// comes from the fixed allowlist in protocol/signin.go, keyed by provider. No
// caller supplies an argv, and an unknown provider is refused.

// RaiseSignIn notifies the chief of staff that a known CLI on one machine needs
// the owner's login (decision 0196). It grants nothing and stores no ask row.
func (s *Store) RaiseSignIn(node, provider, reason string) (protocol.Message, bool, error) {
	info, ok := protocol.SignInProvider(provider)
	if !ok {
		return protocol.Message{}, false, reject("unknown sign-in provider")
	}
	host := s.hostname(node)
	if host == "" {
		host = node
	}
	if strings.TrimSpace(reason) == "" {
		reason = info.Reason
	}
	body := fmt.Sprintf("Sign in to %s on %s\nDetail: %s\nWhy only the owner: Only you can complete this login.\n"+
		"If the owner agrees, the CTO opens it with sign_in_open machine %s provider %s, quoting the owner's words.",
		info.Label, host, reason, host, info.Provider)
	return s.sendToChief(s.db, agentNetSender, "sign_in:"+node+":"+info.Provider, body)
}

// signInPrepare asks a named machine to open the login in its own herdr. It
// checks owner authority before any frame goes to the target machine, and the
// provider must be on the allowlist.
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

// signInRaise sends one sign-in notice to the chief for a known provider on a
// machine. The caller may name a provider, never a command. An empty machine
// means the node that sent the frame.
func (g *Gateway) signInRaise(ctx context.Context, nc *nodeConn, raw []byte) (json.RawMessage, error) {
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
	m, sent, err := g.store.RaiseSignIn(node, info.Provider, p.Reason)
	if err != nil {
		return nil, err
	}
	if sent {
		g.dispatch(ctx, m)
	}
	g.logLine("sign_in.raise", "node", node, "provider", info.Provider, "message", m.ID)
	return json.Marshal(map[string]any{"ok": true, "node": node, "provider": info.Provider, "message_id": m.ID})
}

// signInClose logs that a provider's login remedy is gone on the sending node.
func (g *Gateway) signInClose(nc *nodeConn, raw []byte) error {
	var p protocol.SignInClose
	if json.Unmarshal(raw, &p) != nil || p.Provider == "" {
		return reject("sign_in_close needs a provider")
	}
	if _, ok := protocol.SignInProvider(p.Provider); !ok {
		return reject("unknown sign-in provider")
	}
	g.logLine("sign_in.close", "node", nc.id, "provider", p.Provider, "reason", p.Reason)
	return nil
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
