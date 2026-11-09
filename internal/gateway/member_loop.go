package gateway

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The "Members:" part of a lead's loop tick (decision 0111). A lead of a team
// with asks_to=lead sees which members are blocked or waiting, and which
// workers its members started have ended or still wait for review. It is
// built from data agent-net already has, for a due tick only.

// memberLines is the most lines the part shows for each of its two lists.
const memberLines = 4

// memberReport is the lines of the Members part, already worded.
type memberReport struct {
	blocked, workers []string
}

func (r memberReport) none() bool { return len(r.blocked)+len(r.workers) == 0 }

// text is the part's lines, one per member or worker.
func (r memberReport) text() string {
	var lines []string
	for _, list := range [][]string{r.blocked, r.workers} {
		for i, l := range list {
			if i == memberLines {
				lines = append(lines, fmt.Sprintf("- +%d more", len(list)-memberLines))
				break
			}
			lines = append(lines, "- "+l)
		}
	}
	return strings.Join(lines, "\n")
}

// leadMemberSessions finds the sessions of the members that send their
// questions to this lead: the sessions of the repos and agents in the teams it
// leads with asks_to=lead. The lead's own sessions and workers are not
// members. Each candidate is checked with askerTeam, the lookup that routes
// the member's asks, so the two never disagree about who a lead's members are.
func (s *Store) leadMemberSessions(lead protocol.AgentView) (map[string]bool, error) {
	if lead.AgentID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT DISTINCT s.id FROM teams t JOIN team_members tm ON tm.team_id=t.id
		JOIN sessions s ON (tm.member_kind='repo' AND tm.member_id<>'' AND tm.member_id=s.repo_id) OR (tm.member_kind='agent' AND tm.member_id=s.agent_id)
		WHERE t.active=1 AND t.asks_to=? AND t.owner_agent_id=? AND (t.lead_session IS NULL OR t.lead_session=?)
		AND s.archived_at=0 AND s.worker='' AND s.harness NOT IN ('owner','cto','bot') AND s.id<>?`,
		protocol.AsksToLead, lead.AgentID, lead.SessionID, lead.SessionID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, id := range ids {
		_, leadAgent, _, ok, err := s.askerTeam(id)
		if err != nil {
			return nil, err
		}
		if ok && leadAgent == lead.AgentID {
			out[id] = true
		}
	}
	return out, nil
}

// memberBlockedSince says when a member began to read as blocked or waiting.
// ok is false when it does not. The task counts when it reads blocked, and an
// explicit wait counts too; harness prompts do not, they end with the turn.
func memberBlockedSince(a protocol.AgentView) (since int64, text string, ok bool) {
	var waiting *protocol.Waiting
	if a.Waiting != nil && a.Waiting.Explicit {
		waiting = a.Waiting
	}
	text, ok = blockedReading(a.Profile.Task, waiting)
	if !ok {
		return 0, "", false
	}
	if taskReadsBlocked(a.Profile.Task) {
		since = a.TaskStarted
	}
	if waiting != nil && waiting.Since > 0 && (since == 0 || waiting.Since < since) {
		since = waiting.Since
	}
	return since, text, true
}

// loopMembers builds the Members part for one lead's tick: the members that
// read as blocked or waiting, and the workers they started that ended since
// the last tick or whose change has no passed accept step. agents is the list
// the tick already loaded.
func (s *Store) loopMembers(lead protocol.AgentView, agents []protocol.AgentView, sinceUnix int64, now time.Time) (memberReport, error) {
	var r memberReport
	members, err := s.leadMemberSessions(lead)
	if err != nil || len(members) == 0 {
		return r, err
	}
	labels := map[string]string{}
	type blockedMember struct {
		since int64
		line  string
	}
	var blocked []blockedMember
	for _, a := range agents {
		if !members[a.SessionID] {
			continue
		}
		label := firstNonEmpty(a.AgentName, a.Profile.Name, a.Title, a.SessionID)
		labels[a.SessionID] = label
		if !a.Online {
			continue
		}
		since, text, ok := memberBlockedSince(a)
		if !ok {
			continue
		}
		line := label + " is blocked or waiting"
		if since > 0 {
			line = label + " blocked or waiting for " + durationText(now.Sub(time.Unix(since, 0)))
		}
		blocked = append(blocked, blockedMember{since, line + ": " + protocol.NeutralizeMarkers(askFirstLine(text, 120))})
	}
	sort.SliceStable(blocked, func(i, j int) bool {
		if (blocked[i].since == 0) != (blocked[j].since == 0) {
			return blocked[j].since == 0
		}
		return blocked[i].since < blocked[j].since
	})
	for _, b := range blocked {
		r.blocked = append(r.blocked, b.line)
	}
	r.workers, err = s.memberWorkerLines(members, labels, sinceUnix, now)
	return r, err
}

// memberWorkerLines lists the workers that members started and that ended
// since the last tick, or that ended with a change nobody has accepted. A
// change is accepted when its current revision has a passed accept step.
func (s *Store) memberWorkerLines(members map[string]bool, labels map[string]string, sinceUnix int64, now time.Time) ([]string, error) {
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, sinceUnix)
	rows, err := s.db.Query(`SELECT r.session_id,r.caller_session_id,r.ended_at,COALESCE(c.id,'') FROM worker_runs r
		LEFT JOIN delivery_changes c ON c.state='open' AND json_extract(c.origin,'$.kind')='worker' AND json_extract(c.origin,'$.session_id')=r.session_id
			AND NOT EXISTS (SELECT 1 FROM delivery_evidence e WHERE e.change_id=c.id AND e.revision=c.revision AND e.step='accept' AND e.result='pass' AND e.after_merge=0)
		WHERE r.session_id<>'' AND r.caller_session_id IN (`+marks+`) AND (r.ended_at>=? OR c.id IS NOT NULL)
		ORDER BY r.ended_at DESC,r.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var lines []string
	for rows.Next() {
		var worker, caller, change string
		var ended int64
		if err := rows.Scan(&worker, &caller, &ended, &change); err != nil {
			return nil, err
		}
		if seen[worker] {
			continue
		}
		seen[worker] = true
		line := fmt.Sprintf("worker %s (%s) ended %s ago", worker, firstNonEmpty(labels[caller], caller), durationText(now.Sub(time.Unix(ended, 0))))
		if change != "" {
			line += ", change " + change + " not accepted yet"
		}
		lines = append(lines, line)
	}
	return lines, rows.Err()
}
