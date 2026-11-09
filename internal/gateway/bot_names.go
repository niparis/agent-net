package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/niparis/agent-net/internal/protocol"
)

// A bot app may act under several names (decision 0118). Each distinct name of
// an app has its own bot session and its own agent node, so a message shows who
// sent it and a reply comes back to that name only. A call without a name keeps
// the app's shared session. The name is a label, not a security boundary: every
// name of an app has the same token and the same grants.
//
// A named session is a row of sessions with harness "bot", node_id the app ID
// and native_id "as:<key>", where key is the name folded to lower case. Its
// agent node is a row of agents with role "bot" and the ID "<app ID>/<key>", so
// the node is a child of the app's node by its ID. Triggers (botNamesTriggers)
// rename and archive these nodes with the app.

const (
	botNameMaxLen   = 32
	botNameHeader   = "X-Agent-Net-As"
	botNameClientAs = "as:"
	botNativePrefix = "as:"
)

// parseBotName checks a name and returns it cleaned (spaces trimmed and
// collapsed) with its key (lower case). A name is 1 to 32 characters: letters,
// digits, space, - and _.
func parseBotName(raw string) (name, key string, err error) {
	bad := fmt.Errorf("as must be 1 to %d characters: letters, digits, space, - and _", botNameMaxLen)
	if utf8.RuneCountInString(raw) > botNameMaxLen {
		return "", "", bad
	}
	for _, r := range raw {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_') {
			return "", "", bad
		}
	}
	name = strings.Join(strings.Fields(raw), " ")
	if name == "" {
		return "", "", bad
	}
	return name, strings.ToLower(name), nil
}

// botCallName finds the name a call acts under: the per-call as argument, else
// the X-Agent-Net-As header, else a clientInfo name written "as:<name>". An
// empty result is the shared session. A clientInfo name without the "as:"
// marker is an ordinary client name (such as "Cursor") and is ignored, so no
// existing bot changes session.
func botCallName(in map[string]any, req *mcp.CallToolRequest) (string, error) {
	if v, ok := in["as"]; ok {
		s, isString := v.(string)
		if !isString {
			return "", fmt.Errorf("as must be a string")
		}
		return s, nil
	}
	if req != nil && req.Extra != nil {
		if h := headerValue(req.Extra.Header, botNameHeader); h != "" {
			return h, nil
		}
	}
	if req != nil && req.Session != nil {
		if p := req.Session.InitializeParams(); p != nil && p.ClientInfo != nil {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(p.ClientInfo.Name), botNameClientAs); ok {
				return rest, nil
			}
		}
	}
	return "", nil
}

func headerValue(h http.Header, name string) string {
	if h == nil {
		return ""
	}
	return strings.TrimSpace(h.Get(name))
}

// touchBotNamed returns the session of one name of an active bot app, and
// creates it, with its agent node, on first use.
func (s *Store) touchBotNamed(app App, raw string) (string, error) {
	name, key, err := parseBotName(raw)
	if err != nil {
		return "", err
	}
	now := s.now().UnixNano()
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var appName string
	if err = tx.QueryRow(`SELECT name FROM apps WHERE id=? AND kind='bot' AND revoked_at IS NULL`, app.ID).Scan(&appName); err != nil {
		return "", fmt.Errorf("app is not active")
	}
	agent := app.ID + "/" + key
	display := name + " (" + appName + ")"
	if _, err = tx.Exec(`INSERT INTO agents(id,display_name,role,created_at,updated_at,tombstone) VALUES(?,?,'bot',?,?,0) ON CONFLICT(id) DO NOTHING`, agent, display, now, now); err != nil {
		return "", err
	}
	id := protocol.NewID("s")
	if _, err = tx.Exec(`INSERT INTO sessions (id,node_id,harness,native_id,name,status,last_seen,last_active,online,created_at,agent_id)
		VALUES (?,?,'bot',?,?,'idle',?,?,1,?,?)
		ON CONFLICT (node_id,harness,native_id) DO UPDATE SET last_seen=excluded.last_seen, last_active=excluded.last_active, online=1, agent_id=excluded.agent_id`,
		id, app.ID, botNativePrefix+key, display, now, now, now, agent); err != nil {
		return "", err
	}
	if err = tx.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness='bot' AND native_id=?`, app.ID, botNativePrefix+key).Scan(&id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// botNames lists the names each app has used, sorted, without the app suffix.
func (s *Store) botNames() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT s.node_id, s.name, a.name FROM sessions s JOIN apps a ON a.id=s.node_id WHERE s.harness='bot' AND substr(s.native_id,1,3)='as:' ORDER BY lower(s.name), s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var app, display, appName string
		if err = rows.Scan(&app, &display, &appName); err != nil {
			return nil, err
		}
		out[app] = append(out[app], strings.TrimSuffix(display, " ("+appName+")"))
	}
	return out, rows.Err()
}

// botNamesTriggers keep the named nodes and sessions in step with their app,
// like the triggers of decision 0092 do for the app's own node: a rename
// changes the suffix, and revoking archives the nodes (tombstone) and takes
// them out of their Teams. Nothing is deleted.
const botNamesTriggers = `
CREATE TRIGGER IF NOT EXISTS bot_names_rename AFTER UPDATE OF name ON apps WHEN NEW.kind='bot' AND NEW.name<>OLD.name BEGIN
 UPDATE agents SET display_name=substr(display_name,1,length(display_name)-length(OLD.name)-3)||' ('||NEW.name||')',
  updated_at=CAST(strftime('%s','now') AS INTEGER)*1000000000
  WHERE role='bot' AND substr(id,1,length(NEW.id)+1)=NEW.id||'/' AND substr(display_name,-(length(OLD.name)+3))=' ('||OLD.name||')';
 UPDATE sessions SET name=substr(name,1,length(name)-length(OLD.name)-3)||' ('||NEW.name||')'
  WHERE harness='bot' AND node_id=NEW.id AND substr(native_id,1,3)='as:' AND substr(name,-(length(OLD.name)+3))=' ('||OLD.name||')';
END;
CREATE TRIGGER IF NOT EXISTS bot_names_revoke AFTER UPDATE OF revoked_at ON apps WHEN NEW.kind='bot' AND OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL BEGIN
 INSERT INTO team_history(action,team_id,old_team_id,member_kind,member_id,old_value,new_value,at,actor)
  SELECT 'membership','',team_id,'agent',member_id,team_id,'',NEW.revoked_at,'apps' FROM team_members WHERE member_kind='agent' AND substr(member_id,1,length(NEW.id)+1)=NEW.id||'/';
 DELETE FROM team_members WHERE member_kind='agent' AND substr(member_id,1,length(NEW.id)+1)=NEW.id||'/';
 UPDATE agents SET tombstone=1, updated_at=NEW.revoked_at WHERE role='bot' AND substr(id,1,length(NEW.id)+1)=NEW.id||'/';
END;
`

const botNamesMigration = "-- A bot app may act under several names (decision 0118).\n" + botNamesTriggers
