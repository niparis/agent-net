package gateway

import (
	"database/sql"
	"encoding/json"

	"github.com/niparis/agent-net/internal/protocol"
)

// A connected bot app is an ordinary agent node with role "bot" (decision
// 0092). Its agent ID is the app ID, so the node needs no second key. Triggers
// on the apps table keep the node in step with the app, whichever code path
// approves, renames or revokes it:
//   - approval of a bot app creates the node, named after the app
//   - a rename of the app renames the node
//   - revoking the app archives the node (tombstone) and takes it out of its
//     team; nothing is deleted
//
// The bot's sessions link to the node through sessions.agent_id, which touchBot
// sets, so its charter and place resolve with the code agents use.
const botAgentTriggers = `
CREATE TRIGGER IF NOT EXISTS bot_agent_insert AFTER INSERT ON apps WHEN NEW.kind='bot' AND NEW.revoked_at IS NULL BEGIN
 INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(NEW.id,NEW.name,'bot',NEW.created_at,NEW.created_at,0) ON CONFLICT(id) DO NOTHING;
END;
CREATE TRIGGER IF NOT EXISTS bot_agent_rename AFTER UPDATE OF name ON apps WHEN NEW.kind='bot' BEGIN
 UPDATE agents SET display_name=NEW.name, updated_at=CAST(strftime('%s','now') AS INTEGER)*1000000000 WHERE id=NEW.id AND role='bot';
END;
CREATE TRIGGER IF NOT EXISTS bot_agent_revoke AFTER UPDATE OF revoked_at ON apps WHEN NEW.kind='bot' AND OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL BEGIN
 INSERT INTO team_history(action,team_id,old_team_id,member_kind,member_id,old_value,new_value,at,actor)
  SELECT 'membership','',team_id,'agent',NEW.id,team_id,'',NEW.revoked_at,'apps' FROM team_members WHERE member_kind='agent' AND member_id=NEW.id;
 DELETE FROM team_members WHERE member_kind='agent' AND member_id=NEW.id;
 UPDATE agents SET tombstone=1, updated_at=NEW.revoked_at WHERE id=NEW.id AND role='bot';
END;
`

// botAgentBackfill gives every active bot app that has no node one, and links
// the bot's sessions to it. It is safe to run again.
const botAgentBackfill = `
INSERT OR IGNORE INTO agents(id,display_name,role,created_at,updated_at,tombstone)
 SELECT id,name,'bot',created_at,created_at,0 FROM apps WHERE kind='bot' AND revoked_at IS NULL;
UPDATE sessions SET agent_id=node_id
 WHERE harness='bot' AND agent_id IS NULL AND node_id IN (SELECT id FROM agents WHERE role='bot' AND tombstone=0);
`

const botAgentMigration = "-- Bots are agents (decision 0092).\n" + botAgentTriggers + botAgentBackfill

// errCallsEdge is the answer to any edit of a "calls" edge. The edge is derived
// from the bot's grants on read, so only a grant changes it.
var errCallsEdge = reject("a calls edge comes from the bot's grants; change the grant in Connected apps")

// refuseBotLead stops a bot from becoming the owner or lead of a Team or Repo.
// A bot reads no charter by itself and cannot take orders, so it cannot lead.
func refuseBotLead(tx *sql.Tx, agent string) error {
	var bot bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND role='bot')`, agent).Scan(&bot); err != nil {
		return err
	}
	if bot {
		return reject("a bot can never be a Team or Repo lead")
	}
	return nil
}

// addBotGraph fills the bot details of the bot nodes and adds the derived
// "calls" edges. Both come from the apps table and the shared MCP registry,
// read once for all bots, so a Graph read costs the same however many bots
// there are. The edges are never stored; the grant is the only way to change
// them.
//
// An edge goes from the bot to the project the granted server belongs to: the
// one project of the server's assignment, or else the project named like the
// server. A server with neither draws no edge (the Graph has no machine
// nodes); the node's "can call" list still names it.
func (s *Store) addBotGraph(g *protocol.Graph, byRef map[protocol.GraphRef]*protocol.Node) error {
	rows, err := s.db.Query(`SELECT id,client_name,created_at,last_used_at,grants FROM apps WHERE kind='bot' AND revoked_at IS NULL ORDER BY created_at,id`)
	if err != nil {
		return err
	}
	type bot struct {
		id     string
		grants []string
	}
	var bots []bot
	for rows.Next() {
		var id, client, raw string
		var created int64
		var used sql.NullInt64
		if err = rows.Scan(&id, &client, &created, &used, &raw); err != nil {
			rows.Close()
			return err
		}
		var grants []string
		if err = json.Unmarshal([]byte(raw), &grants); err != nil {
			rows.Close()
			return err
		}
		n := byRef[protocol.GraphRef{Kind: "agent", ID: id}]
		if n == nil {
			continue
		}
		n.Bot = &protocol.NodeBot{Client: client, ConnectedAt: created, LastUsedAt: used.Int64}
		bots = append(bots, bot{id, grants})
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(bots) == 0 {
		return err
	}
	var servers map[string]protocol.MCPServer
	var assigned map[string]protocol.ToolAssignment
	for _, b := range bots {
		n := byRef[protocol.GraphRef{Kind: "agent", ID: b.id}]
		for _, grant := range b.grants {
			name, ok := protocol.ParseMCPServerGrant(grant)
			if !ok {
				continue
			}
			n.Bot.CanCall = append(n.Bot.CanCall, name)
			if servers == nil {
				list, err := s.MCPServers()
				if err != nil {
					return err
				}
				servers = map[string]protocol.MCPServer{}
				for _, srv := range list {
					servers[srv.Name] = srv
				}
				if assigned, err = s.MCPAssignments(); err != nil {
					return err
				}
			}
			if srv, found := servers[name]; !found || !srv.Enabled {
				continue
			}
			repo := ""
			if a := assigned[name]; a.Scope == "projects" && len(a.Projects) == 1 {
				repo = a.Projects[0]
			}
			if byRef[protocol.GraphRef{Kind: "repo", ID: repo}] == nil {
				repo = name
			}
			if byRef[protocol.GraphRef{Kind: "repo", ID: repo}] == nil {
				continue
			}
			from, to := protocol.GraphRef{Kind: "agent", ID: b.id}, protocol.GraphRef{Kind: "repo", ID: repo}
			g.Edges = append(g.Edges, protocol.Edge{From: from.ID, To: to.ID, Kind: protocol.EdgeCalls, FromRef: &from, ToRef: &to})
		}
	}
	return nil
}
