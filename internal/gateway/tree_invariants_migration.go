package gateway

import (
	"database/sql"
	"fmt"
	"slices"
)

// TreeInvariantsMarker names the migration of decision 0193 part 2. The
// migration runner calls applyTreeInvariantsTx when it reaches it.
const TreeInvariantsMarker = "Session tree invariants I1 I2 I4"

// treeInvariantsMigration applies I1, I2 and I4 of decision 0193 once to the
// stored data. The work is in applyTreeInvariantsTx; the text only marks the
// place in the list. Appended at the end and never edited once it ships.
const treeInvariantsMigration = `SELECT 1 /* ` + TreeInvariantsMarker + ` */;`

// applyTreeInvariantsTx brings stored data to the state the writes now keep:
//
//   - I1: every Agent that leads something gets one lead session by the I1
//     order, and an Agent that leads nothing loses its pin.
//   - I4: an offline session that holds a lead Agent but is not its lead
//     session (an old copy, a replaced record) is archived. An Agent whose
//     sessions are all offline has no lead session and keeps them.
//   - I2: a live session that holds a lead Agent but is not its lead session
//     moves to its Repo's member Agent, created if the Repo has none.
//
// The open CTO questions about which session leads are closed: the rule
// decides now. Each change is one line of the returned report and one line
// of the Desk log, so the CTO can undo any single one with lead_set or
// session_agent_set.
func (s *Store) applyTreeInvariantsTx(tx *sql.Tx) ([]string, error) {
	var report []string
	pins := func() (map[string]string, error) {
		out := map[string]string{}
		rows, err := tx.Query(`SELECT id, COALESCE(lead_session,'') FROM agents WHERE tombstone=0`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var agent, pin string
			if err := rows.Scan(&agent, &pin); err != nil {
				return nil, err
			}
			out[agent] = pin
		}
		return out, rows.Err()
	}
	before, err := pins()
	if err != nil {
		return nil, err
	}
	for agent := range before {
		if err := s.keepLeadTx(tx, agent); err != nil {
			return nil, err
		}
	}
	after, err := pins()
	if err != nil {
		return nil, err
	}
	for agent, pin := range after {
		switch old := before[agent]; {
		case old == pin:
		case pin == "":
			report = append(report, fmt.Sprintf("Agent %s: no lead session (was %s)", agent, old))
		case old == "":
			report = append(report, fmt.Sprintf("Agent %s: lead session %s", agent, pin))
		default:
			report = append(report, fmt.Sprintf("Agent %s: lead session %s (was %s)", agent, pin, old))
		}
	}
	slices.Sort(report)

	type extra struct {
		id, agent, repo string
		archive         bool
	}
	var extras []extra
	rows, err := tx.Query(`SELECT s.id, s.agent_id, COALESCE(s.repo_id,''), s.online=0 AND s.pinned_at=0
		FROM sessions s JOIN agents a ON a.id=s.agent_id
		WHERE s.agent_id IN (` + leadingAgentsSQL + `) AND COALESCE(a.lead_session,'') NOT IN ('',s.id)
		AND s.worker='' AND s.archived_at=0 AND s.harness NOT IN ('owner','cto','bot')
		AND NOT EXISTS(SELECT 1 FROM repos pm WHERE pm.merger_session=s.id)
		ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e extra
		if err := rows.Scan(&e.id, &e.agent, &e.repo, &e.archive); err != nil {
			rows.Close()
			return nil, err
		}
		extras = append(extras, e)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	now := s.now().UnixNano()
	for _, e := range extras {
		if e.archive {
			if _, err := tx.Exec(`UPDATE sessions SET archived_at=? WHERE id=?`, now, e.id); err != nil {
				return nil, err
			}
			report = append(report, fmt.Sprintf("Session %s: archived, an offline copy of lead Agent %s", e.id, e.agent))
			continue
		}
		var repoLead string
		if e.repo != "" {
			if err := tx.QueryRow(`SELECT COALESCE(lead_agent_id,'') FROM repos WHERE id=?`, e.repo).Scan(&repoLead); err != nil && err != sql.ErrNoRows {
				return nil, err
			}
		}
		if repoLead == "" {
			report = append(report, fmt.Sprintf("Session %s: kept on lead Agent %s, its Repo has no lead to take a member Agent from", e.id, e.agent))
			continue
		}
		member, err := s.memberAgentTx(tx, e.repo, repoLead)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE sessions SET agent_id=? WHERE id=?`, member, e.id); err != nil {
			return nil, err
		}
		report = append(report, fmt.Sprintf("Session %s: moved from lead Agent %s to member Agent %s", e.id, e.agent, member))
	}

	res, err := tx.Exec(`UPDATE inbox_items SET status='answered', answer='Decided by rule I1 of decision 0193', answered_at=?
		WHERE status='open' AND source_ref LIKE 'lead_session:%'`, now)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		report = append(report, fmt.Sprintf("Closed %d CTO question(s) about which session leads", n))
	}
	for _, line := range report {
		if err := s.deskLog(tx, "gateway", "session_tree", "", "Decision 0193 migration: "+line, "", nil); err != nil {
			return nil, err
		}
	}
	return report, nil
}
