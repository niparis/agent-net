package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

const nudgePauseMigration = `
CREATE TABLE IF NOT EXISTS nudge_pauses (
 id TEXT PRIMARY KEY,
 kind TEXT NOT NULL,
 team_id TEXT NOT NULL DEFAULT '',
 agent_id TEXT NOT NULL DEFAULT '',
 until_at INTEGER NOT NULL DEFAULT 0,
 provider_reset INTEGER NOT NULL DEFAULT 0,
 reason TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 created_by TEXT NOT NULL,
 ended_at INTEGER NOT NULL DEFAULT 0,
 ended_by TEXT NOT NULL DEFAULT '',
 receipt TEXT NOT NULL,
 undo_action TEXT NOT NULL,
 CHECK ((team_id='') <> (agent_id=''))
);
CREATE INDEX IF NOT EXISTS nudge_pauses_active ON nudge_pauses(kind,agent_id,team_id) WHERE ended_at=0;
CREATE INDEX IF NOT EXISTS nudge_sessions_limited ON sessions(agent_id,limited_until);
CREATE INDEX IF NOT EXISTS nudge_repo_sessions_limited ON sessions(repo_id,limited_until);
`

const nudgePausedSQL = `SELECT EXISTS(SELECT 1 FROM nudge_pauses p JOIN sessions dst ON dst.id=?
 WHERE p.ended_at=0 AND p.kind=?
 AND (p.agent_id=dst.agent_id AND p.agent_id<>'' OR p.team_id IN
 (SELECT team_id FROM team_members WHERE (member_kind='agent' AND member_id=dst.agent_id) OR (member_kind='repo' AND member_id=dst.repo_id)))
 AND (p.until_at=0 OR p.until_at>?)
 AND (p.provider_reset=0 OR EXISTS(SELECT 1 FROM sessions lim WHERE lim.limited_until>?
 AND lim.agent_id IN (SELECT p.agent_id WHERE p.agent_id<>'' UNION
 SELECT member_id FROM team_members WHERE member_kind='agent' AND team_id=p.team_id)
 OR lim.repo_id IN (SELECT member_id FROM team_members WHERE member_kind='repo' AND team_id=p.team_id) AND lim.limited_until>?)))`

// One indexed lookup per candidate, before its message or bookkeeping exists.
// Scope follows agent identity and current team membership, across machines.
// Expiry predicates also apply between maintenance ticks.
func (s *Store) nudgePaused(q askDB, kind, recipient string) (bool, error) {
	var paused bool
	now := s.now()
	err := q.QueryRow(nudgePausedSQL, recipient, kind, now.UnixNano(), now.Unix(), now.Unix()).Scan(&paused)
	return paused, err
}

// Preserve ended rows. provider_reset has only a seven-day safety expiry;
// an ordinary pause has no cap. Called once per tick and before mutations/read.
func (s *Store) expireNudgePauses(q askDB) error {
	now := s.now()
	_, err := q.Exec(`UPDATE nudge_pauses SET ended_at=CASE WHEN until_at>0 AND until_at<=? THEN until_at ELSE ? END,
 ended_by=CASE WHEN until_at>0 AND until_at<=? THEN CASE WHEN provider_reset=1 THEN 'provider_reset_cap' ELSE 'expired' END ELSE 'provider_reset' END
 WHERE ended_at=0 AND ((until_at>0 AND until_at<=?) OR (provider_reset=1 AND NOT EXISTS
 (SELECT 1 FROM sessions lim WHERE lim.limited_until>? AND
 lim.agent_id IN (SELECT nudge_pauses.agent_id WHERE nudge_pauses.agent_id<>'' UNION
 SELECT member_id FROM team_members WHERE member_kind='agent' AND team_id=nudge_pauses.team_id)
 OR lim.repo_id IN (SELECT member_id FROM team_members WHERE member_kind='repo' AND team_id=nudge_pauses.team_id) AND lim.limited_until>?)))`, now.UnixNano(), now.UnixNano(), now.UnixNano(), now.UnixNano(), now.Unix(), now.Unix())
	return err
}
func nudgeChief(q askDB, node, session string) error {
	chief, err := liveChief(q)
	if err != nil {
		return err
	}
	if chief == "" || chief != session {
		return reject("only the chief of staff may manage nudge pauses")
	}
	var machine string
	if err := q.QueryRow(`SELECT node_id FROM sessions WHERE id=?`, session).Scan(&machine); err != nil {
		return err
	}
	if machine != node {
		return reject("the chief of staff is not on this machine")
	}
	return nil
}
func nudgeTarget(q askDB, a protocol.NudgePauseArgs) (team, agent string, err error) {
	var rows *sql.Rows
	if a.Team != "" {
		rows, err = q.Query(`SELECT id FROM teams WHERE active=1 AND (id=? OR name=?)`, a.Team, a.Team)
	} else {
		rows, err = q.Query(`SELECT id FROM agents WHERE tombstone=0 AND (id=? OR display_name=?)`, a.Agent, a.Agent)
	}
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return "", "", err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return "", "", err
	}
	if len(ids) != 1 {
		return "", "", reject("target must resolve to exactly one active team or agent (found %d)", len(ids))
	}
	if a.Team != "" {
		return ids[0], "", nil
	}
	return "", ids[0], nil
}

var nudgePrefixes = map[string]string{
	"idle_check": "check:", "autonomy_continue": "wake:", "reply_reminder": "reminder:", "loop_tick": "loop:",
	"stuck_step": "delivery:stuck:", "undelivered": "undelivered:", "waiting_notice": "waiting:", "wake_failed": "wake-failed:", "member_blocked": "member_blocked:",
}

// Match the gateway's existing reserved-sender keys. Step keys are random, so
// their reminder header identifies them, including batched reminders.
func nudgeMessageMatch(kind string) (string, []any) {
	if kind == "step_reminder" {
		return `m.body LIKE '[agent-net step task reminder%'`, nil
	}
	return `m.idem_key LIKE ?`, []any{nudgePrefixes[kind] + "%"}
}
func (s *Store) PauseNudge(node string, r protocol.NudgePauseRequest) (protocol.NudgeResult, error) {
	out := protocol.NudgeResult{}
	if err := r.Validate(); err != nil {
		return out, reject("%v", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = nudgeChief(tx, node, r.SessionID); err != nil {
		return out, err
	}
	team, agent, err := nudgeTarget(tx, r.NudgePauseArgs)
	if err != nil {
		return out, err
	}
	scope := `s.agent_id=? AND ?<>'' OR EXISTS (SELECT 1 FROM team_members tm WHERE tm.team_id=? AND ((tm.member_kind='agent' AND tm.member_id=s.agent_id) OR (tm.member_kind='repo' AND tm.member_id=s.repo_id)))`
	rows, err := tx.Query(`SELECT s.id,COALESCE(NULLIF(p.name,''),NULLIF(s.name,''),s.id) FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE `+scope+` ORDER BY s.id`, agent, agent, team)
	if err != nil {
		return out, err
	}
	out.Sessions = []protocol.NudgeSession{}
	for rows.Next() {
		var x protocol.NudgeSession
		if err = rows.Scan(&x.ID, &x.Name); err != nil {
			rows.Close()
			return out, err
		}
		out.Sessions = append(out.Sessions, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	match, args := nudgeMessageMatch(r.Kind)
	params := []any{s.now().Add(-24 * time.Hour).UnixNano(), s.now().UnixNano(), agent, agent, team}
	params = append(params, args...)
	err = tx.QueryRow(`SELECT COUNT(*) FROM messages m JOIN sessions s ON s.id=m.recipient WHERE m.sender='agent-net' AND m.created_at>=? AND m.created_at<=? AND (`+scope+`) AND `+match, params...).Scan(&out.Messages24h)
	if err != nil {
		return out, err
	}
	if r.Mode == "preview" {
		return out, nil
	} // Rollback, including expiry: preview writes nothing.
	var until int64
	reset := r.Until == "provider_reset"
	if reset {
		until = s.now().Add(7 * 24 * time.Hour).UnixNano()
	} else if r.Until != "" {
		at, _ := protocol.ParseNudgeUntil(r.Until)
		until = at.UnixNano()
		if until <= s.now().UnixNano() {
			return out, reject("until must be in the future")
		}
	}
	if err = s.expireNudgePauses(tx); err != nil {
		return out, err
	}
	out.ID = protocol.NewID("np")
	receipt := protocol.NewID("nu")
	_, err = tx.Exec(`INSERT INTO nudge_pauses(id,kind,team_id,agent_id,until_at,provider_reset,reason,created_at,created_by,receipt,undo_action) VALUES(?,?,?,?,?,?,?,?,?,?,'pause')`, out.ID, r.Kind, team, agent, until, boolInt(reset), r.Reason, s.now().UnixNano(), r.SessionID, receipt)
	if err != nil {
		return out, err
	}
	p := protocol.NudgePause{ID: out.ID, Kind: r.Kind, Team: team, Agent: agent, Until: r.Until, Reason: r.Reason}
	// A nudge already waiting in the delivery queue must not outlive a pause.
	// Its old bookkeeping remains intact; resume never replays it.
	cancelArgs := []any{agent, agent, team}
	cancelArgs = append(cancelArgs, args...)
	if _, err = tx.Exec(`UPDATE messages AS m SET state='expired' WHERE m.sender='agent-net'
 AND m.state IN ('stored','dispatched','failed','held') AND m.recipient IN (SELECT s.id FROM sessions s WHERE `+scope+`) AND `+match, cancelArgs...); err != nil {
		return out, err
	}
	if err = s.logNudge(tx, p, "Paused", receipt); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Store) logNudge(tx *sql.Tx, p protocol.NudgePause, action, receipt string) error {
	target := "agent " + p.Agent
	if p.Team != "" {
		target = "team " + p.Team
	}
	until := p.Until
	if until == "" {
		until = "no expiry"
	}
	text := fmt.Sprintf("%s %s for %s; until %s; reason: %s", action, p.Kind, target, until, p.Reason)
	var options []string
	if receipt != "" {
		raw, _ := json.Marshal(protocol.NudgeUndoArgs{ID: p.ID, Receipt: receipt})
		options = []string{protocol.OpNudgeUndo, string(raw)}
	}
	return s.deskLog(tx, "chief", "nudge_pause", p.ID, text, text, options)
}
func readNudge(q askDB, id string) (protocol.NudgePause, string, string, error) {
	var p protocol.NudgePause
	var until int64
	var reset bool
	var receipt, action string
	err := q.QueryRow(`SELECT id,kind,team_id,agent_id,until_at,provider_reset,reason,created_at,ended_at,ended_by,receipt,undo_action FROM nudge_pauses WHERE id=?`, id).Scan(&p.ID, &p.Kind, &p.Team, &p.Agent, &until, &reset, &p.Reason, &p.CreatedAt, &p.EndedAt, &p.EndedBy, &receipt, &action)
	if reset {
		p.Until = "provider_reset"
	} else if until != 0 {
		p.Until = time.Unix(0, until).UTC().Format(time.RFC3339)
	}
	return p, receipt, action, err
}
func (s *Store) ResumeNudge(node string, r protocol.NudgeResumeRequest) (protocol.NudgeResult, error) {
	out := protocol.NudgeResult{ID: r.ID}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = nudgeChief(tx, node, r.SessionID); err != nil {
		return out, err
	}
	if err = s.expireNudgePauses(tx); err != nil {
		return out, err
	}
	p, _, _, err := readNudge(tx, r.ID)
	if err != nil {
		return out, reject("unknown pause")
	}
	if p.EndedAt != 0 {
		return out, reject("pause has already ended")
	}
	receipt := protocol.NewID("nu")
	_, err = tx.Exec(`UPDATE nudge_pauses SET ended_at=?,ended_by=?,receipt=?,undo_action='resume' WHERE id=?`, s.now().UnixNano(), r.SessionID, receipt, r.ID)
	if err != nil {
		return out, err
	}
	if err = s.logNudge(tx, p, "Resumed", receipt); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Store) FindNudgePauses(node, session string) (protocol.NudgeResult, error) {
	out := protocol.NudgeResult{Pauses: []protocol.NudgePause{}}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = nudgeChief(tx, node, session); err != nil {
		return out, err
	}
	if err = s.expireNudgePauses(tx); err != nil {
		return out, err
	}
	rows, err := tx.Query(`SELECT id FROM nudge_pauses ORDER BY created_at DESC,id`)
	if err != nil {
		return out, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		p, _, _, err := readNudge(tx, id)
		if err != nil {
			return out, err
		}
		out.Pauses = append(out.Pauses, p)
	}
	return out, tx.Commit()
}
func (s *Store) undoNudge(a protocol.NudgeUndoArgs) (protocol.NudgeResult, error) {
	out := protocol.NudgeResult{ID: a.ID}
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	p, receipt, action, err := readNudge(tx, a.ID)
	if err != nil || receipt == "" || receipt != a.Receipt {
		return out, reject("unknown, stale or already undone receipt")
	}
	if action == "pause" {
		if p.EndedAt != 0 {
			return out, reject("pause has already ended")
		}
		_, err = tx.Exec(`UPDATE nudge_pauses SET ended_at=?,ended_by='owner_undo',receipt='',undo_action='' WHERE id=?`, s.now().UnixNano(), a.ID)
	} else if action == "resume" {
		_, err = tx.Exec(`UPDATE nudge_pauses SET ended_at=0,ended_by='',receipt='',undo_action='' WHERE id=?`, a.ID)
	} else {
		return out, reject("receipt cannot be undone")
	}
	if err != nil {
		return out, err
	}
	if err = s.expireNudgePauses(tx); err != nil {
		return out, err
	}
	if err = s.logNudge(tx, p, "Owner undid "+action+" of", ""); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) nudgeLookup(q askDB, kind string) func(string) (bool, error) {
	type result struct {
		paused bool
		err    error
	}
	cache := map[string]result{}
	return func(recipient string) (bool, error) {
		if r, ok := cache[recipient]; ok {
			return r.paused, r.err
		}
		paused, err := s.nudgePaused(q, kind, recipient)
		cache[recipient] = result{paused, err}
		return paused, err
	}
}

// queuedNudgeKind identifies only gateway-built messages, never peer or owner
// traffic. First step notices, step results, worker notices and chief wakes
// have no matching key/header.
func queuedNudgeKind(m protocol.Message) string {
	if m.From != "agent-net" {
		return ""
	}
	if strings.HasPrefix(m.Body, "[agent-net step task reminder") {
		return "step_reminder"
	}
	for kind, prefix := range nudgePrefixes {
		if strings.HasPrefix(m.IdempotencyKey, prefix) {
			return kind
		}
	}
	return ""
}

// Re-check at delivery as membership or pauses may change after creation.
// Discard suppressed messages rather than retaining a backlog for resume.
func (s *Store) allowNudgeDelivery(m protocol.Message) (bool, error) {
	kind := queuedNudgeKind(m)
	if kind == "" {
		return true, nil
	}
	now := s.now()
	var state string
	var paused bool
	// Also check durable state so a queue snapshot taken before a pause can
	// never resurrect an expired nudge after resume. Both reads are indexed.
	err := s.db.QueryRow(`SELECT state, (`+nudgePausedSQL+`) FROM messages WHERE id=?`, m.To, kind, now.UnixNano(), now.Unix(), now.Unix(), m.ID).Scan(&state, &paused)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if state != "stored" && state != "dispatched" && state != "failed" && state != "held" {
		return false, nil
	}
	if !paused {
		return true, nil
	}
	_, err = s.db.Exec(`UPDATE messages SET state='expired' WHERE id=? AND state IN ('stored','dispatched','failed','held')`, m.ID)
	return false, err
}
