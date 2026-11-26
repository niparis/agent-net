package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The self-check of decision 0193. The writes keep rules I1 to I4 in their own
// transactions (lead_session.go). Once a minute this check looks for a
// violation that slipped past them, with one query per rule. It only reports:
// one log line and one Desk item for the agent-net lead per cause. It never
// repairs data, because a repair here would hide the write that broke the rule.

// treeCheckEvery is how often the self-check runs.
const treeCheckEvery = time.Minute

// treeCheckRefPrefix starts the source key of a self-check Desk item:
// tree:<rule>:<agent or session id>. The key is what keeps one open item per
// cause.
const treeCheckRefPrefix = "tree:"

// treeViolation is one cause the self-check found.
type treeViolation struct {
	// rule is I1 to I4; kind is "agent" or "session", the kind of id.
	rule, kind, id, text string
}

func (v treeViolation) ref() string { return treeCheckRefPrefix + v.rule + ":" + v.id }

// treeSessionName is how a Desk line names a session, on sessions alias x.
func treeSessionName(x string) string {
	return `COALESCE(NULLIF(` + x + `.name,''),` + x + `.id)`
}

// treeLiveCandidate is a lead candidate that is live: online and not stood
// down. Only such a session counts for I1 (I4: offline ones do not).
var treeLiveCandidate = leadCandidateOfSQL("a.id") + ` AND s.online=1 AND s.stood_down=''`

// treeCheckQueries holds one query per rule. Each returns the kind and id of
// the subject and the Desk line. I4 takes its retention cutoff as arguments.
var treeCheckQueries = []struct{ rule, sql string }{
	// I1: an Agent that leads something and has a live session has a live
	// lead session; an Agent that leads nothing has none. A lead session
	// that is archived or replaced is I3's cause, not I1's.
	{"I1", `SELECT 'agent', a.id, CASE
		WHEN l.agent IS NULL THEN 'Agent '||a.id||' leads nothing but has lead session '||a.lead_session
		WHEN COALESCE(a.lead_session,'')='' THEN 'Agent '||a.id||' has a live session but no lead session'
		ELSE 'Agent '||a.id||' has a live session, but its lead session '||a.lead_session||' is not live' END
		FROM agents a LEFT JOIN (` + leadingAgentsSQL + `) l ON l.agent=a.id
		WHERE a.tombstone=0 AND a.id<>'cto' AND (
			(l.agent IS NULL AND COALESCE(a.lead_session,'')<>'')
			OR (l.agent IS NOT NULL
				AND EXISTS(SELECT 1 FROM sessions s WHERE ` + treeLiveCandidate + `)
				AND NOT EXISTS(SELECT 1 FROM sessions s WHERE s.id=a.lead_session AND ` + treeLiveCandidate + `)
				AND NOT EXISTS(SELECT 1 FROM sessions p WHERE p.id=a.lead_session AND (p.archived_at<>0 OR p.replaced_by<>''))))`},
	// I2: no live session holds a lead Agent that has another live lead
	// session.
	{"I2", `SELECT 'session', s.id, 'Session '||` + treeSessionName("s") + `||' holds lead Agent '||a.id||', which already has live lead session '||` + treeSessionName("l") + `
		FROM agents a JOIN sessions l ON l.id=a.lead_session JOIN sessions s ON ` + treeLiveCandidate + `
		WHERE a.tombstone=0 AND a.id IN (` + leadingAgentsSQL + `)
			AND l.agent_id=a.id AND l.online=1 AND l.archived_at=0 AND l.replaced_by='' AND l.stood_down='' AND s.id<>l.id`},
	// I3: no live session has a parent that is archived or replaced. A
	// worker's parent is its starter (rule 1). Every other parent is an
	// Agent's lead session, so a lead session that is archived or replaced
	// leaves the sessions under it without their parent.
	{"I3", `SELECT 'session', s.id, 'Worker '||` + treeSessionName("s") + `||' still runs under '||` + treeSessionName("p") + `||', which is '||CASE WHEN p.replaced_by<>'' THEN 'replaced' ELSE 'archived' END
		FROM sessions s JOIN sessions p ON p.id=(CASE WHEN json_valid(s.worker) THEN json_extract(s.worker,'$.owner') END)
		WHERE s.online=1 AND s.worker<>'' AND s.archived_at=0 AND s.replaced_by='' AND (p.archived_at<>0 OR p.replaced_by<>'')
		UNION ALL
		SELECT 'agent', a.id, 'Lead session '||` + treeSessionName("p") + `||' of Agent '||a.id||' is '||CASE WHEN p.replaced_by<>'' THEN 'replaced' ELSE 'archived' END||', so the sessions under it have no parent'
		FROM agents a JOIN sessions p ON p.id=a.lead_session
		WHERE a.tombstone=0 AND a.id IN (` + leadingAgentsSQL + `) AND (p.archived_at<>0 OR p.replaced_by<>'')`},
	// I4: no offline, unpinned session of a lead Agent stays unarchived past
	// the retention. The condition is the one AutoArchiveSessions archives by.
	{"I4", `SELECT 'session', s.id, 'Offline session '||` + treeSessionName("s") + `||' of lead Agent '||s.agent_id||' is past the retention and not archived'
		FROM sessions s
		WHERE s.online=0 AND s.last_seen<?1 AND s.archived_at=0 AND s.pinned_at=0 AND s.worker='' AND s.owner_used_at<?1
			AND MAX(s.last_active,s.last_seen,s.created_at)<?1 AND s.harness NOT IN ('owner','cto','bot')
			AND s.id<>COALESCE((SELECT value FROM gateway_state WHERE key='cto_session'),'')
			AND NOT EXISTS(SELECT 1 FROM repos pm WHERE pm.merger_session=s.id AND pm.merger_session<>'')
			AND s.agent_id IN (` + leadingAgentsSQL + `)`},
}

// treeI4Cutoff is the last-seen time before which an offline session of a
// lead Agent should be archived. The archive runs once per RetentionInterval
// and skips its first pass after a start, so the check waits two intervals
// past the retention before it calls a session late.
func treeI4Cutoff(now time.Time) int64 {
	return now.Add(-autoArchiveAfter - 2*RetentionInterval).UnixNano()
}

// treeViolations runs the four queries and returns every cause found, in
// rule order.
func (s *Store) treeViolations(now time.Time) ([]treeViolation, error) {
	var out []treeViolation
	for _, q := range treeCheckQueries {
		var args []any
		if q.rule == "I4" {
			args = []any{treeI4Cutoff(now)}
		}
		rows, err := s.db.Query(q.sql, args...)
		if err != nil {
			return nil, fmt.Errorf("tree check %s: %w", q.rule, err)
		}
		for rows.Next() {
			v := treeViolation{rule: q.rule}
			if err := rows.Scan(&v.kind, &v.id, &v.text); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, v)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// checkTree is one pass of the self-check. A cause with no open Desk item
// gets one, for the agent-net lead, and one log line event=tree.violation. A
// cause whose item is open is left alone. An open item whose cause is gone
// is resolved. It returns the causes found.
func (s *Store) checkTree() ([]treeViolation, error) {
	found, err := s.treeViolations(s.now())
	if err != nil {
		return nil, err
	}
	open := map[string]bool{}
	rows, err := s.db.Query(`SELECT source_ref FROM inbox_items WHERE status='open' AND source_ref LIKE ?`, treeCheckRefPrefix+"%")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, err
		}
		open[ref] = true
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	lead := ""
	for _, v := range found {
		ref := v.ref()
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if open[ref] {
			continue
		}
		if lead == "" {
			if lead, err = s.agentNetLeadSession(); err != nil {
				return nil, err
			}
		}
		s.logf("tree.violation", "rule", v.rule, v.kind, v.id)
		about := ""
		if v.kind == "session" {
			about = protocol.AskAboutSession
		}
		if _, err := s.createInboxItem(s.db, protocol.InboxItem{
			Kind: protocol.AskKindCheck, SessionID: gatewayAsker, Node: "agent-net", FromKind: protocol.AskFromGateway, AskerName: "agent-net",
			SourceRef: ref, AboutKind: about, AboutID: v.id, NextWho: lead,
			Text:           "Session tree rule " + v.rule + ": " + v.text,
			WhyYou:         "The tree self-check of decision 0193 found it: a write skipped this rule.",
			Recommendation: "Find the write that broke the rule and fix it. lead_set or session_agent_set fixes the data; this item closes by itself once the cause is gone.",
		}); err != nil {
			return nil, err
		}
	}
	for ref := range open {
		if seen[ref] {
			continue
		}
		if err := s.closeAskRef(s.db, ref, protocol.AskReasonResolved); err != nil {
			return nil, err
		}
		s.logf("tree.resolved", "ref", ref)
	}
	return found, nil
}

// agentNetLeadSession is the session of the agent-net lead that gets the
// self-check items: the lead session of the agent-net Repo's lead Agent, or
// its latest session. Empty when there is none; the item then goes to the
// chief, as any check does.
func (s *Store) agentNetLeadSession() (string, error) {
	var repo string
	if err := s.db.QueryRow(`SELECT id FROM repos WHERE id='agent-net' OR display_name='agent-net' ORDER BY id='agent-net' DESC LIMIT 1`).Scan(&repo); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	agent, err := repoTopAgentQ(s.db, repo)
	if err != nil || agent == "" {
		return "", err
	}
	id, _, err := s.leadAskSession(agent)
	return id, err
}

// RunTreeCheck runs the self-check once a minute until ctx ends. The first
// pass waits one interval, so the daemons have reconnected and their sessions
// are online again.
func (g *Gateway) RunTreeCheck(ctx context.Context) {
	t := time.NewTicker(treeCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.appsDone:
			return
		case <-t.C:
			func() {
				defer g.startLoop("tree_check")()
				if _, err := g.store.checkTree(); err != nil && ctx.Err() == nil {
					g.logLine("error", "where", "tree check", "err", err)
				}
			}()
		}
	}
}
