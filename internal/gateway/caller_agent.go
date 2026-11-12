package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A step owned by the caller belongs to the caller's agent, not only to the
// session that submitted the change (decision 0148). Any live session of that
// agent may claim and report it. When no session of the agent is live, the
// steps that waited past delivery.step_stuck wake the agent; if that fails the
// repo lead gets them.
//
// The SQL below is written for two aliases: c is delivery_changes and s is the
// session that asks. x is an inner session.

const callerEscalationMigration = `
-- One row per caller agent (or caller session without an agent) that has
-- steps waiting past the stuck limit and no live session. woken_at is when the
-- wake was tried; lead_offered says the repo lead may act on the steps.
CREATE TABLE IF NOT EXISTS caller_step_escalations (
 owner_key TEXT PRIMARY KEY, woken_at INTEGER NOT NULL, lead_offered INTEGER NOT NULL DEFAULT 0);
`

// ownsOtherStepSQL is true when session a owns a step of change c that is not
// a caller step: it is the project's merger for the change's machine, or the
// node that owns a node step of the change. Such a session does its own step.
// It is never handed the caller's steps, and may not act on them, so one
// session does not accept, test and merge the same change. It uses the same
// identity checks as the step dispatch.
func ownsOtherStepSQL(a string) string {
	return `(EXISTS(SELECT 1 FROM project_mergers pm WHERE pm.project=c.project AND pm.node_id=c.machine AND pm.session_id=` + a + `.id)
 OR EXISTS(SELECT 1 FROM delivery_step_tasks o WHERE o.change_id=c.id AND o.owner_node NOT LIKE '/%'
 AND o.owner_node=COALESCE(` + a + `.repo_id,NULLIF(` + a + `.repo_project,''),(SELECT pr.project FROM profiles pr WHERE pr.session_id=` + a + `.id),'')))`
}

var (
	// liveSessionSQL is a session that can act on delivery steps.
	liveSessionSQL = `x.online=1 AND x.worker='' AND x.harness NOT IN ('owner','bot','cto')`
	// callerAgentSQL is the agent of the change's caller session, or ''.
	callerAgentSQL = `COALESCE((SELECT a.agent_id FROM sessions a WHERE a.id=c.caller),'')`
	// callerOwnerKeySQL names who owns the caller's steps: the agent, or the
	// session when it has no agent.
	callerOwnerKeySQL = `COALESCE(NULLIF(` + callerAgentSQL + `,''),c.caller)`
	// agentSessionSQL is a live session of the caller's agent that may act on
	// the caller's steps: it owns no other step of the change.
	agentSessionSQL = liveSessionSQL + ` AND ` + callerAgentSQL + `<>'' AND x.agent_id=` + callerAgentSQL + ` AND NOT ` + ownsOtherStepSQL("x")
	// callerOwnerLiveSQL is true when a live session owns the caller's steps.
	callerOwnerLiveSQL = `EXISTS(SELECT 1 FROM sessions x WHERE (x.id=c.caller AND ` + liveSessionSQL + `) OR (` + agentSessionSQL + `))`
	// liveCallerSessionSQL is the one live session of the owner that gets the
	// work: the caller itself, else the agent's most recently seen session.
	liveCallerSessionSQL = `COALESCE((SELECT x.id FROM sessions x WHERE x.id=c.caller AND ` + liveSessionSQL + `),
 (SELECT x.id FROM sessions x WHERE ` + agentSessionSQL + ` ORDER BY x.last_seen DESC,x.id LIMIT 1))`
	// repoLeadAgentSQL is the agent that leads the change's repo.
	repoLeadAgentSQL = `(CASE WHEN EXISTS(SELECT 1 FROM repo_leads rl WHERE rl.repo_id=c.project) THEN (SELECT rl.agent_id FROM repo_leads rl WHERE rl.repo_id=c.project) ELSE (SELECT r.owner_agent_id FROM repos r WHERE r.id=c.project) END)`
	// leadOfferedSQL is true when the owner's steps were offered to the lead.
	leadOfferedSQL = `EXISTS(SELECT 1 FROM caller_step_escalations e WHERE e.lead_offered=1 AND e.owner_key=` + callerOwnerKeySQL + `)`
	// leadSessionSQL is the one live lead session that gets offered steps. A
	// pinned lead session is the only one.
	leadSessionSQL = `(SELECT x.id FROM sessions x WHERE ` + leadOfferedSQL + ` AND x.agent_id=` + repoLeadAgentSQL + ` AND ` + liveSessionSQL + ` AND NOT ` + ownsOtherStepSQL("x") + `
 AND COALESCE((SELECT rl.lead_session FROM repo_leads rl WHERE rl.repo_id=c.project),x.id)=x.id ORDER BY x.last_seen DESC,x.id LIMIT 1)`
	// offeredCallerSessionSQL is the one session a ready caller step is sent to.
	offeredCallerSessionSQL = `COALESCE(` + liveCallerSessionSQL + `,` + leadSessionSQL + `)`
	// sessionActsForCallerSQL says that session s may claim and report the
	// caller's steps of change c: the caller, any session of its agent, or the
	// repo lead once the steps were offered to it.
	sessionActsForCallerSQL = `(s.id=c.caller OR (` + callerAgentSQL + `<>'' AND s.agent_id=` + callerAgentSQL + ` AND NOT ` + ownsOtherStepSQL("s") + `) OR
 (` + leadOfferedSQL + ` AND NOT ` + callerOwnerLiveSQL + ` AND s.agent_id=` + repoLeadAgentSQL + ` AND NOT ` + ownsOtherStepSQL("s") + `
 AND COALESCE((SELECT rl.lead_session FROM repo_leads rl WHERE rl.repo_id=c.project),s.id)=s.id))`
)

// sessionActsForCaller says whether a session may claim or report a step that
// the caller owns. The session that already holds the claim always may.
func sessionActsForCaller(q deliveryQuerier, c protocol.Change, ref protocol.StepRef, session string) (bool, error) {
	var claimed string
	err := q.QueryRow(`SELECT claimed_by FROM delivery_step_tasks
		WHERE change_id=? AND revision=? AND step=? AND for_step=? AND after_merge=? AND completed=0`,
		c.ID, c.Revision, ref.Step, ref.For, boolInt(ref.AfterMerge)).Scan(&claimed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if claimed != "" && claimed == session {
		return true, nil
	}
	var ok bool
	err = q.QueryRow(`SELECT EXISTS(SELECT 1 FROM delivery_changes c, sessions s WHERE c.id=? AND s.id=? AND `+sessionActsForCallerSQL+`)`,
		c.ID, session).Scan(&ok)
	return ok, err
}

// waitingCallerTask is one unclaimed caller step that has no live owner.
type waitingCallerTask struct {
	changeID, title, project, caller, ownerKey string
	ref                                        protocol.StepRef
	createdAt                                  int64
}

// unownedCallerTasks lists the ready caller steps that nobody claimed since
// cutoff and that have no live owner, oldest first.
func (s *Store) unownedCallerTasks(cutoff int64) ([]waitingCallerTask, error) {
	rows, err := s.db.Query(`SELECT t.change_id,c.title,c.project,c.caller,`+callerOwnerKeySQL+`,t.step,t.for_step,t.after_merge,t.created_at
		FROM delivery_step_tasks t JOIN delivery_changes c ON c.id=t.change_id AND c.revision=t.revision
		WHERE `+s.stepNotWaitingAt()+` AND t.completed=0 AND t.claimed_by='' AND t.owner_node=?
		AND ((t.after_merge=0 AND c.state='open') OR (t.after_merge=1 AND c.state='merged'))
		AND c.caller<>'' AND t.created_at<=? AND NOT `+callerOwnerLiveSQL+`
		ORDER BY t.created_at,t.change_id,t.rowid`, callerTaskOwner, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []waitingCallerTask
	for rows.Next() {
		var t waitingCallerTask
		var after int
		if err := rows.Scan(&t.changeID, &t.title, &t.project, &t.caller, &t.ownerKey, &t.ref.Step, &t.ref.For, &after, &t.createdAt); err != nil {
			return nil, err
		}
		t.ref.AfterMerge = after != 0
		out = append(out, t)
	}
	return out, rows.Err()
}

// escalateCallerSteps handles caller steps that waited past the stuck limit
// with no live session of the caller's agent. It wakes the agent once with the
// list of its waiting steps. When that cannot be done, or the agent has not
// come online when the wake window ends, the repo lead may act on them.
func (g *Gateway) escalateCallerSteps(ctx context.Context, stuckLimit time.Duration) {
	if stuckLimit <= 0 {
		return
	}
	now := g.store.now()
	tasks, err := g.store.unownedCallerTasks(now.Add(-stuckLimit).Unix())
	if err != nil {
		g.logLine("error", "where", "unowned caller steps", "err", err)
		return
	}
	var keys []string
	groups := map[string][]waitingCallerTask{}
	for _, t := range tasks {
		if _, ok := groups[t.ownerKey]; !ok {
			keys = append(keys, t.ownerKey)
		}
		groups[t.ownerKey] = append(groups[t.ownerKey], t)
	}
	// An owner that is live again, or has nothing waiting, starts clean.
	if err := g.store.dropCallerEscalations(keys); err != nil {
		g.logLine("error", "where", "caller escalations cleanup", "err", err)
		return
	}
	for _, key := range keys {
		var wokenAt int64
		var offered int
		err := g.store.db.QueryRow(`SELECT woken_at,lead_offered FROM caller_step_escalations WHERE owner_key=?`, key).Scan(&wokenAt, &offered)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			g.wakeCallerAgent(ctx, key, groups[key])
		case err != nil:
			g.logLine("error", "where", "caller escalation read", "err", err)
		case offered == 0 && now.Sub(time.Unix(0, wokenAt)) >= wakeTimeout:
			if _, err := g.store.db.Exec(`UPDATE caller_step_escalations SET lead_offered=1 WHERE owner_key=?`, key); err != nil {
				g.logLine("error", "where", "caller escalation offer", "err", err)
			}
		}
	}
}

func (s *Store) dropCallerEscalations(keep []string) error {
	if len(keep) == 0 {
		_, err := s.db.Exec(`DELETE FROM caller_step_escalations`)
		return err
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(keep)), ",")
	args := make([]any, len(keep))
	for i, k := range keep {
		args[i] = k
	}
	_, err := s.db.Exec(`DELETE FROM caller_step_escalations WHERE owner_key NOT IN (`+marks+`)`, args...)
	return err
}

// wakeCallerAgent sends the list of waiting steps to the best offline session
// of the owner, which wakes it the way any message does. The attempt is
// recorded once. No session to wake, or no wake started, offers the steps to
// the repo lead at once.
func (g *Gateway) wakeCallerAgent(ctx context.Context, key string, tasks []waitingCallerTask) {
	now := g.store.now()
	// A session that is online but owns another step of the change (it is the
	// merger, say) may not act on these steps. The agent is still not offline,
	// so "no session of yours was online" would be false.
	var live bool
	if err := g.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions x WHERE (x.id=? OR x.agent_id=?) AND `+liveSessionSQL+`)`, key, key).Scan(&live); err != nil {
		g.logLine("error", "where", "caller agent live check", "err", err)
		return
	}
	if live {
		// Nobody can be woken, and the live session may not act on the steps,
		// so the repo lead may act on them at once.
		if _, err := g.store.db.Exec(`INSERT OR IGNORE INTO caller_step_escalations(owner_key,woken_at,lead_offered) VALUES(?,?,1)`, key, now.UnixNano()); err != nil {
			g.logLine("error", "where", "caller escalation write", "err", err)
		}
		return
	}
	var target string
	err := g.store.db.QueryRow(`SELECT s.id FROM sessions s JOIN delivery_changes c ON c.id=?
		WHERE s.online=0 AND s.worker='' AND s.harness IN ('claude','codex','opencode') AND s.archived_at=0 AND s.replaced_by=''
		AND NOT EXISTS(SELECT 1 FROM session_wake_failures f WHERE f.session_id=s.id)
		AND (s.id=? OR (s.agent_id=? AND NOT `+ownsOtherStepSQL("s")+`))
		ORDER BY CASE WHEN s.id=? THEN 0 ELSE 1 END,s.last_seen DESC,s.id LIMIT 1`,
		tasks[0].changeID, tasks[0].caller, key, tasks[0].caller).Scan(&target)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		g.logLine("error", "where", "caller agent wake target", "err", err)
		return
	}
	started := false
	if target != "" {
		m := protocol.Message{
			ID: protocol.NewID("m"), ConversationID: protocol.NewID("c"),
			From: "agent-net", FromName: "agent-net", FromHarness: "agent-net", To: target,
			Type: protocol.TypeNotification, Body: callerWakeBody(tasks, now),
			IdempotencyKey: fmt.Sprintf("delivery:agentwake:%s:%d", key, now.Unix()),
			CreatedAt:      now.UTC(), ExpiresAt: now.Add(protocol.DefaultTTL).UTC(), State: protocol.StateStored,
		}
		if _, err := g.store.db.Exec(`INSERT OR IGNORE INTO messages(id,conversation_id,parent_id,sender,recipient,type,body,hop,idem_key,created_at,expires_at,state)
			VALUES(?,?,?,?,?,?,?,0,?,?,?,?)`, m.ID, m.ConversationID, m.ParentID, m.From, m.To, m.Type, m.Body,
			m.IdempotencyKey, m.CreatedAt.UnixNano(), m.ExpiresAt.UnixNano(), m.State); err != nil {
			g.logLine("error", "where", "caller agent wake message", "err", err)
			return
		}
		g.store.logMessage(m)
		started = g.dispatch(ctx, m) || g.wakeInFlight(target)
	}
	offered := 0
	if !started {
		offered = 1
	}
	if _, err := g.store.db.Exec(`INSERT OR IGNORE INTO caller_step_escalations(owner_key,woken_at,lead_offered) VALUES(?,?,?)`,
		key, now.UnixNano(), offered); err != nil {
		g.logLine("error", "where", "caller escalation write", "err", err)
	}
}

// callerWakeBody lists the steps that wait for the owner, oldest first.
func callerWakeBody(tasks []waitingCallerTask, now time.Time) string {
	const shown = 20
	var b strings.Builder
	fmt.Fprintf(&b, "[agent-net waiting steps]\n%d delivery steps wait for you and no session of yours was online. Claim and report each one:\n", len(tasks))
	for i, t := range tasks {
		if i == shown {
			fmt.Fprintf(&b, "- and %d more\n", len(tasks)-shown)
			break
		}
		title := protocol.NeutralizeMarkers(t.title)
		if title == "" {
			title = t.changeID
		}
		fmt.Fprintf(&b, "- %s (%s): %s, waiting %s\n", title, t.changeID, t.ref.String(), formatAge(now.Sub(time.Unix(t.createdAt, 0))))
	}
	return strings.TrimRight(b.String(), "\n")
}

// waitingWorkAfter is how long finished work may wait with no live owner
// before the Desk shows a line for its project.
const waitingWorkAfter = 24 * time.Hour

const waitingWorkRef = "waiting-work:"

// syncWaitingWorkAsks keeps one Desk line per project for finished changes that
// wait with no live owner, and closes it when the count reaches zero. The
// line is updated in place. A line the owner dismissed stays quiet for a day.
func (g *Gateway) syncWaitingWorkAsks() {
	s := g.store
	now := s.now()
	tasks, err := s.unownedCallerTasks(now.Add(-waitingWorkAfter).Unix())
	if err != nil {
		g.logLine("error", "where", "waiting work", "err", err)
		return
	}
	type project struct {
		changes map[string]bool
		steps   map[string]bool
		oldest  int64
	}
	byProject := map[string]*project{}
	var names []string
	for _, t := range tasks {
		p := byProject[t.project]
		if p == nil {
			p = &project{changes: map[string]bool{}, steps: map[string]bool{}, oldest: t.createdAt}
			byProject[t.project] = p
			names = append(names, t.project)
		}
		// A step handed out with its partner shares the change; only the
		// first one, the one that is ready, names the wait.
		if !p.changes[t.changeID] {
			p.steps[t.ref.String()] = true
		}
		p.changes[t.changeID] = true
		if t.createdAt < p.oldest {
			p.oldest = t.createdAt
		}
	}
	sort.Strings(names)
	for _, name := range names {
		p := byProject[name]
		ref := waitingWorkRef + name
		var dismissed bool
		if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM inbox_items WHERE source_ref=? AND status<>'open' AND answer<>? AND answered_at>?)`,
			ref, protocol.AskReasonResolved, now.Add(-waitingWorkAfter).UnixNano()).Scan(&dismissed); err != nil {
			g.logLine("error", "where", "waiting work dismissed", "err", err)
			continue
		}
		if dismissed {
			continue
		}
		lead, err := s.repoLeadLiveness(name)
		if err != nil {
			g.logLine("error", "where", "waiting work lead", "err", err)
			continue
		}
		changes := "changes"
		if len(p.changes) == 1 {
			changes = "change"
		}
		step := "steps"
		if len(p.steps) == 1 {
			for k := range p.steps {
				step = k
			}
		}
		text := fmt.Sprintf("%s: %d finished %s waiting for %s, oldest %s, %s", name, len(p.changes), changes, step,
			waitingWorkAge(now.Sub(time.Unix(p.oldest, 0))), lead)
		if _, err := s.createInboxItem(s.db, protocol.InboxItem{
			Kind: protocol.AskKindCheck, SessionID: gatewayAsker, Node: name, FromKind: protocol.AskFromGateway, AskerName: "agent-net",
			SourceRef: ref, AboutKind: protocol.AskAboutProject, AboutID: name, NextWho: protocol.AskNextOwner,
			Text:           text,
			WhyYou:         "Finished work has no live session of its agent to accept it.",
			Recommendation: "Start a session of the project's agent so it can accept the changes.",
		}); err != nil {
			g.logLine("error", "where", "waiting work ask", "err", err)
		}
	}
	rows, err := s.db.Query(`SELECT source_ref FROM inbox_items WHERE status='open' AND source_ref LIKE ?`, waitingWorkRef+"%")
	if err != nil {
		g.logLine("error", "where", "waiting work open asks", "err", err)
		return
	}
	var stale []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			g.logLine("error", "where", "waiting work open asks", "err", err)
			return
		}
		if byProject[strings.TrimPrefix(ref, waitingWorkRef)] == nil {
			stale = append(stale, ref)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		g.logLine("error", "where", "waiting work open asks", "err", err)
		return
	}
	for _, ref := range stale {
		if err := s.closeAskRef(s.db, ref, protocol.AskReasonResolved); err != nil {
			g.logLine("error", "where", "waiting work close", "err", err)
		}
	}
}

// repoLeadLiveness says in a few words whether the repo's lead has a live session.
func (s *Store) repoLeadLiveness(repo string) (string, error) {
	var lead sql.NullString
	if err := s.db.QueryRow(`SELECT CASE WHEN EXISTS(SELECT 1 FROM repo_leads WHERE repo_id=?) THEN (SELECT agent_id FROM repo_leads WHERE repo_id=?)
		ELSE (SELECT owner_agent_id FROM repos WHERE id=?) END`, repo, repo, repo).Scan(&lead); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if !lead.Valid || lead.String == "" {
		return "no lead", nil
	}
	var live bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions x WHERE `+liveSessionSQL+` AND x.agent_id=?)`, lead.String).Scan(&live); err != nil {
		return "", err
	}
	if live {
		return "lead online", nil
	}
	return "lead offline", nil
}

// waitingWorkAge writes an age in whole hours below two days, else in days.
func waitingWorkAge(d time.Duration) string {
	if d < 48*time.Hour {
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	}
	return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
}
