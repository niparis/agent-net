package gateway

import (
	"context"
	"fmt"
	"strings"

	delegated "github.com/niparis/agent-net/internal/authority"
	"github.com/niparis/agent-net/internal/protocol"
)

// A scope holder gets owner asks from inside its area before the chief
// (decision 0166). The order is: the owner's words (not an ask at all), then a
// role node that holds the asks scope over the asker's repo, then the chief,
// then the Desk. A repo no asks scope covers behaves exactly as before. The
// holder is a live session of that node; it closes the ask the way a session
// the chief sent an ask to does (agent_ask_sort close).

// asksHolderSession returns the live session of a role node that holds the asks
// scope over the repo of the asker's session. It never returns the asker's own
// node or the chief's node, and returns "" when no such session is online.
func (s *Store) asksHolderSession(q askDB, asker string) string {
	repo, askerAgent, _, worker, err := sessionIdentityQuery(q, asker)
	if err != nil || repo == "" || worker {
		return ""
	}
	chief, _ := chiefSession(q)
	var chiefAgent string
	if chief != "" {
		_ = q.QueryRow(`SELECT COALESCE(agent_id,'') FROM sessions WHERE id=?`, chief).Scan(&chiefAgent)
	}
	scopes, err := s.AllScopes()
	if err != nil {
		return ""
	}
	tried := map[string]bool{}
	for _, sc := range scopes {
		if sc.Kind != protocol.ScopeAsks || sc.Node == askerAgent || sc.Node == chiefAgent || tried[sc.Node] {
			continue
		}
		tried[sc.Node] = true
		mine, repos, err := s.ScopesWithRepos(sc.Node)
		if err != nil || !(delegated.Held{Scopes: mine, Repos: repos}).Holds(protocol.ScopeAsks, repo) {
			continue
		}
		var session string
		err = q.QueryRow(`SELECT id FROM sessions WHERE agent_id=? AND online=1 AND archived_at=0 AND worker='' AND harness NOT IN (?,?) AND id<>? ORDER BY last_seen DESC LIMIT 1`,
			sc.Node, protocol.OwnerHarness, "cto", asker).Scan(&session)
		if err == nil && session != "" {
			return session
		}
	}
	return ""
}

// holderAskBody is the message a scope holder gets with an ask.
func (s *Store) holderAskBody(q askDB, item protocol.InboxItem) string {
	return fmt.Sprintf("The owner delegated asks from %s to you, so ask %s reached you before the chief of staff: %s\nAnswer it yourself only if you can, and close it with agent_ask_sort (result close) once it is resolved. Anything that needs the owner goes to them with agent_owner_ask.",
		sessionLabel(q, item.SessionID), item.ID, strings.TrimSpace(item.Text))
}

// flushHolderAsks delivers the messages stored for scope holders. A store call
// cannot dispatch, so a held ask's message waits as stored until this tick,
// which runs every second. The query is one indexed read of the stored
// messages from agent-net.
func (g *Gateway) flushHolderAsks(ctx context.Context) {
	rows, err := g.store.db.Query(`SELECT DISTINCT s.node_id FROM messages m JOIN sessions s ON s.id=m.recipient
		WHERE m.state=? AND m.sender=? AND m.idem_key LIKE ?`, protocol.StateStored, agentNetSender, chiefAskKeyPrefix+"%")
	if err != nil {
		g.logLine("error", "where", "holder asks", "err", err)
		return
	}
	var nodes []string
	for rows.Next() {
		var node string
		if rows.Scan(&node) == nil {
			nodes = append(nodes, node)
		}
	}
	rows.Close()
	for _, node := range nodes {
		g.dispatchPending(ctx, node, false)
	}
}
