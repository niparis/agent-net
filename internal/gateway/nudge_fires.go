package gateway

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// nudgeFollowWindow is how long after a nudge a tool call, an announce, step
// evidence or a message that is not an ack still counts as work following it.
// The owner asked for "a set window" and no setting, so this is a constant.
const nudgeFollowWindow = 30 * time.Minute

const nudgeFiresMigration = `
-- One row per delivered automatic nudge (decision 0134). The agent and team
-- are captured when the nudge is made, so a later move does not rewrite
-- history. followed_at and followed_kind are set once, on read or by a tool
-- touch. checked_at keeps a read from re-testing a closed window.
CREATE TABLE IF NOT EXISTS nudge_fires (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 kind TEXT NOT NULL,
 recipient TEXT NOT NULL,
 agent_id TEXT NOT NULL DEFAULT '',
 team_id TEXT NOT NULL DEFAULT '',
 message_id TEXT NOT NULL,
 fired_at INTEGER NOT NULL,
 followed_at INTEGER NOT NULL DEFAULT 0,
 followed_kind TEXT NOT NULL DEFAULT '',
 checked_at INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS nudge_fires_message ON nudge_fires(message_id);
CREATE INDEX IF NOT EXISTS nudge_fires_recent ON nudge_fires(fired_at, followed_at);
CREATE INDEX IF NOT EXISTS nudge_fires_recipient ON nudge_fires(recipient, followed_at, fired_at);
CREATE INDEX IF NOT EXISTS messages_author_time ON messages(sender, created_at);
CREATE INDEX IF NOT EXISTS delivery_evidence_session ON delivery_evidence(by_session, at);
`

// recordNudgeFire writes one row for a nudge message just stored. It is called
// in the nudge's own transaction. A batched envelope has one row per original
// nudge message, because each part passed through its builder.
func recordNudgeFire(q askDB, kind, recipient, messageID string, at time.Time) error {
	_, err := q.Exec(`INSERT OR IGNORE INTO nudge_fires(kind,recipient,agent_id,team_id,message_id,fired_at)
 SELECT ?,s.id,COALESCE(s.agent_id,''),
  COALESCE((SELECT tm.team_id FROM team_members tm
   JOIN teams t ON t.id=tm.team_id AND t.active=1
   WHERE (tm.member_kind='agent' AND tm.member_id=s.agent_id)
      OR (tm.member_kind='repo' AND tm.member_id=s.repo_id)
   ORDER BY tm.team_id LIMIT 1),''),
  ?,?
 FROM sessions s WHERE s.id=?`, kind, messageID, at.UnixNano(), recipient)
	return err
}

// TouchNudgeWork records one agent-net tool call from a session. It marks the
// fires of that session whose window still contains the call. The update is
// indexed and touches only unfollowed rows, so an ack (which is a message, not
// a tool call) is never seen here.
func (s *Store) TouchNudgeWork(session string, at int64) error {
	if at <= 0 {
		at = s.now().UnixNano()
	}
	_, err := s.db.Exec(`UPDATE nudge_fires SET followed_at=?,followed_kind='tool_call'
 WHERE recipient=? AND followed_at=0 AND fired_at<=? AND ?<=fired_at+?`,
		at, session, at, at, int64(nudgeFollowWindow))
	return err
}

// markNudgeFollows evaluates unfollowed fires in the requested window. Rows
// whose window is still open are checked again on the next read; a closed row
// is checked once and then given checked_at. The event query uses the sender
// time index, the evidence session index and primary-key lookups, so it never
// scans the message table.
func (s *Store) markNudgeFollows(since time.Time) error {
	now := s.now()
	rows, err := s.db.Query(`SELECT id,recipient,fired_at FROM nudge_fires
 WHERE followed_at=0 AND fired_at>=? AND (fired_at+?>? OR checked_at=0)`,
		since.UnixNano(), int64(nudgeFollowWindow), now.UnixNano())
	if err != nil {
		return err
	}
	type fire struct {
		id, firedAt int64
		recipient   string
	}
	var fires []fire
	for rows.Next() {
		var f fire
		if err := rows.Scan(&f.id, &f.recipient, &f.firedAt); err != nil {
			rows.Close()
			return err
		}
		fires = append(fires, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(fires) == 0 {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, f := range fires {
		until := f.firedAt + int64(nudgeFollowWindow)
		if until > now.UnixNano() {
			until = now.UnixNano()
		}
		var kind string
		var at int64
		err := tx.QueryRow(`SELECT kind,at FROM (
 SELECT 'announce' AS kind, updated_at AS at FROM profiles WHERE session_id=?
 UNION ALL
 SELECT 'message', created_at FROM messages WHERE sender=? AND type<>'ack'
 UNION ALL
 SELECT 'step_evidence', at FROM delivery_evidence WHERE by_session=?
) WHERE at>? AND at<=? ORDER BY at LIMIT 1`,
			f.recipient, f.recipient, f.recipient, f.firedAt, until).Scan(&kind, &at)
		if err == nil {
			if _, err = tx.Exec(`UPDATE nudge_fires SET followed_at=?,followed_kind=? WHERE id=?`, at, kind, f.id); err != nil {
				return err
			}
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		if now.UnixNano() >= f.firedAt+int64(nudgeFollowWindow) {
			if _, err := tx.Exec(`UPDATE nudge_fires SET checked_at=? WHERE id=?`, now.UnixNano(), f.id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// nudgeRate is fires and follows for one kind and one agent or team.
type nudgeRate struct {
	kind, agent, team string
	fires, followed   int
}

func (s *Store) nudgeStats(since time.Time, args protocol.NudgeStatsArgs) (protocol.NudgeStatsResult, error) {
	out := protocol.NudgeStatsResult{Since: since.UTC().Format(time.RFC3339)}
	if err := s.markNudgeFollows(since); err != nil {
		return out, err
	}
	now := s.now()
	query := `SELECT kind,agent_id,team_id,COUNT(*),COALESCE(SUM(CASE WHEN followed_at>0 THEN 1 ELSE 0 END),0)
 FROM nudge_fires WHERE fired_at>=? AND fired_at<=?`
	params := []any{since.UnixNano(), now.UnixNano()}
	if args.Kind != "" {
		query += ` AND kind=?`
		params = append(params, args.Kind)
	}
	if args.Agent != "" {
		query += ` AND agent_id IN (SELECT id FROM agents WHERE id=? OR display_name=?)`
		params = append(params, args.Agent, args.Agent)
	}
	if args.Team != "" {
		query += ` AND team_id IN (SELECT id FROM teams WHERE id=? OR name=?)`
		params = append(params, args.Team, args.Team)
	}
	query += ` GROUP BY kind,agent_id,team_id`
	rows, err := s.db.Query(query, params...)
	if err != nil {
		return out, err
	}
	var raw []nudgeRate
	for rows.Next() {
		var r nudgeRate
		if err := rows.Scan(&r.kind, &r.agent, &r.team, &r.fires, &r.followed); err != nil {
			rows.Close()
			return out, err
		}
		raw = append(raw, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	agentNames, teamNames, err := s.nudgeNames()
	if err != nil {
		return out, err
	}
	byAgent := map[string]*nudgeRate{}
	byTeam := map[string]*nudgeRate{}
	for _, r := range raw {
		key := r.kind + "\x00" + r.agent
		if byAgent[key] == nil {
			byAgent[key] = &nudgeRate{kind: r.kind, agent: r.agent}
		}
		byAgent[key].fires += r.fires
		byAgent[key].followed += r.followed
		key = r.kind + "\x00" + r.team
		if byTeam[key] == nil {
			byTeam[key] = &nudgeRate{kind: r.kind, team: r.team}
		}
		byTeam[key].fires += r.fires
		byTeam[key].followed += r.followed
	}
	fill := func(m map[string]*nudgeRate, team bool) []protocol.NudgeStat {
		rows := make([]protocol.NudgeStat, 0, len(m))
		for _, r := range m {
			row := protocol.NudgeStat{Kind: r.kind, Fires: r.fires, Followed: r.followed, UsefulRate: usefulRate(r.fires, r.followed)}
			if team {
				row.Team, row.TeamName = r.team, teamNames[r.team]
				if row.TeamName == "" {
					row.TeamName = "(no team)"
				}
			} else {
				row.Agent, row.AgentName = r.agent, agentNames[r.agent]
				if row.AgentName == "" {
					row.AgentName = "(no agent)"
				}
			}
			rows = append(rows, row)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Fires != rows[j].Fires {
				return rows[i].Fires > rows[j].Fires
			}
			if rows[i].Kind != rows[j].Kind {
				return rows[i].Kind < rows[j].Kind
			}
			return rows[i].Agent+rows[i].Team < rows[j].Agent+rows[j].Team
		})
		return rows
	}
	out.Kinds = fill(byAgent, false)
	out.Teams = fill(byTeam, true)
	for _, row := range out.Kinds {
		out.Fires += row.Fires
		out.Followed += row.Followed
	}
	out.UsefulRate = usefulRate(out.Fires, out.Followed)
	return out, nil
}

func usefulRate(fires, followed int) float64 {
	if fires == 0 {
		return 0
	}
	return math.Round(float64(followed)*10000/float64(fires)) / 10000
}

func (s *Store) nudgeNames() (agents, teams map[string]string, err error) {
	agents, teams = map[string]string{}, map[string]string{}
	rows, err := s.db.Query(`SELECT id,COALESCE(NULLIF(display_name,''),id) FROM agents`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		agents[id] = name
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.db.Query(`SELECT id,COALESCE(NULLIF(name,''),id) FROM teams`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, err
		}
		teams[id] = name
	}
	return agents, teams, rows.Err()
}

// nudgeUseful returns the stored useful rate for one kind and target. It is
// used by the pause preview, which writes nothing and therefore does not run
// the lazy pass.
func (s *Store) nudgeUseful(kind, agent, team string, since time.Time) (fires, followed int, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(CASE WHEN followed_at>0 THEN 1 ELSE 0 END),0)
 FROM nudge_fires WHERE fired_at>=? AND kind=?
 AND ((?<>'' AND agent_id=?) OR (?<>'' AND team_id=?))`,
		since.UnixNano(), kind, agent, agent, team, team).Scan(&fires, &followed)
	return fires, followed, err
}

func (s *Store) NudgeStats(args protocol.NudgeStatsArgs) (protocol.NudgeStatsResult, error) {
	if err := args.Validate(); err != nil {
		return protocol.NudgeStatsResult{}, reject("%v", err)
	}
	now := s.now()
	since := now.Add(-24 * time.Hour)
	if args.Since != "" {
		at, _ := time.Parse(time.RFC3339, args.Since)
		since = at
	}
	if since.After(now) {
		return protocol.NudgeStatsResult{}, reject("since must not be in the future")
	}
	out, err := s.nudgeStats(since, args)
	if err != nil {
		return out, err
	}
	if out.Fires == 0 {
		out.Note = "No nudges fired in this window."
	}
	return out, nil
}

// nudgeUsefulLine is the preview's short line, for example
// "useful 2 of 14 in 24h".
func nudgeUsefulLine(fires, followed int) string {
	return fmt.Sprintf("useful %d of %d in 24h", followed, fires)
}
