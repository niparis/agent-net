package gateway

import (
	"database/sql"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// The Sessions tree is computed by one rule (decision 0193). sessionParent is
// the only code that places a session. The session view carries its answer as
// Parent, and the TUI, the web console, agent_sessions_find and the "your
// place" text read that field. Nothing else guesses a place from teams, repos
// or names.

// placeGraph is what the rule reads: the Agent graph and the live lead session
// of each Agent.
type placeGraph struct {
	// reportsTo is the Agent each Agent reports to in the org tree (decision
	// 0160): protocol.OrgRoot, "cto", another Agent, or empty.
	reportsTo map[string]string
	// lead is the live lead session of each Agent (agents.lead_session, not
	// archived and not replaced). The CTO Agent's lead session is the CTO
	// session the gateway stores (gateway_state cto_session). An Agent without
	// one is missing.
	lead map[string]string
	// leads holds the Agents that lead something (leadingAgentsSQL). Only
	// they have lead sessions; the sessions of any other Agent sit under the
	// lead session of the Agent it reports to.
	leads map[string]bool
	// projectLead is the live lead session of each project's lead Agent. The
	// rule never reads it: it only says where to draw a session the rule
	// could not place.
	projectLead map[string]string
}

// placeSession is the part of a session the rule reads.
type placeSession struct {
	id, agent, project string
	// worker is the session that started this one, for a worker.
	worker string
	// root is set for the owner, the CTO and bots: they sit at the top.
	root bool
}

// sessionParent places one session (decision 0193):
//
//  1. A worker's parent is the session that started it.
//  2. The lead session of an Agent sits under the lead session of the Agent
//     it reports to, so a lead that reports to the CTO sits under the CTO
//     (decision 0184). When that Agent has no live lead session, or the edge
//     points to the owner, the session is a root.
//  3. Any other session of an Agent that leads something sits under its
//     Agent's lead session.
//  4. A session of an Agent that leads nothing (a member or merger Agent)
//     sits under the lead session of the Agent it reports to, beside its
//     peers. It is a root only when that edge points to the owner.
//
// When the rule cannot place a session, noParent names the missing piece:
// "no Agent", "no reports_to" when an Agent that leads nothing reports to no
// one, or "no lead" when the lead session the rule needs is not live. Then
// parent is its project's lead session, so the session is drawn there with a
// marker, not as a separate root.
func sessionParent(s placeSession, g placeGraph) (parent, noParent string) {
	switch {
	case s.worker != "":
		return s.worker, ""
	case s.root:
		return "", ""
	case s.agent == "":
		return g.unplaced(s, "no Agent")
	}
	up := g.reportsTo[s.agent]
	if !g.leads[s.agent] {
		switch {
		case up == protocol.OrgRoot:
			return "", ""
		case up == "":
			return g.unplaced(s, "no reports_to")
		case g.lead[up] != "":
			return g.lead[up], ""
		}
		return g.unplaced(s, "no lead")
	}
	lead := g.lead[s.agent]
	switch {
	case lead == s.id:
		return g.lead[up], ""
	case lead != "":
		return lead, ""
	}
	return g.unplaced(s, "no lead")
}

// unplaced is where a session the rule cannot place is drawn: under its
// project's lead session, or at the top when the project has none.
func (g placeGraph) unplaced(s placeSession, why string) (string, string) {
	parent := g.projectLead[s.project]
	if parent == s.id {
		parent = ""
	}
	return parent, why
}

// loadPlaceGraph reads what sessionParent needs, with the org tree parents the
// caller already computed.
func loadPlaceGraph(q leadDB, reportsTo map[string]string) (placeGraph, error) {
	g := placeGraph{reportsTo: reportsTo, lead: map[string]string{}, leads: map[string]bool{}, projectLead: map[string]string{}}
	rows, err := q.Query(leadingAgentsSQL)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var agent string
		if err := rows.Scan(&agent); err != nil {
			rows.Close()
			return g, err
		}
		g.leads[agent] = true
	}
	if err := rows.Close(); err != nil {
		return g, err
	}
	rows, err = q.Query(`SELECT a.id, a.lead_session FROM agents a JOIN sessions s ON s.id=a.lead_session
		WHERE a.tombstone=0 AND s.archived_at=0 AND s.replaced_by=''`)
	if err != nil {
		return g, err
	}
	for rows.Next() {
		var agent, session string
		if err := rows.Scan(&agent, &session); err != nil {
			rows.Close()
			return g, err
		}
		g.lead[agent] = session
	}
	if err := rows.Close(); err != nil {
		return g, err
	}
	var cto string
	err = q.QueryRow(`SELECT s.id FROM gateway_state st JOIN sessions s ON s.id=st.value
		WHERE st.key='cto_session' AND s.archived_at=0 AND s.replaced_by=''`).Scan(&cto)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return g, err
	}
	if cto != "" {
		g.lead["cto"] = cto
	}
	rows, err = q.Query(`SELECT id, COALESCE(lead_agent_id,'') FROM repos WHERE archived=0`)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var repo, agent string
		if err := rows.Scan(&repo, &agent); err != nil {
			return g, err
		}
		if lead := g.lead[agent]; lead != "" {
			g.projectLead[repo] = lead
		}
	}
	return g, rows.Err()
}

// placeOf is what the rule reads from a session row.
func placeOf(a protocol.AgentView) placeSession {
	s := placeSession{id: a.SessionID, agent: a.AgentID, project: projectOfSession(a),
		root: protocol.IsOwner(a.Harness) || a.Harness == "cto" || a.Harness == "bot"}
	if a.Worker != nil {
		s.worker = a.Worker.Owner
	}
	return s
}

// fillPlacement gives each row the parent the org tree computes for its Agent
// (ReportsTo) and its place in the Sessions tree (Parent, NoParent). The
// one-row read and the list both use it, so a row says the same either way.
func fillPlacement(q leadDB, rows []protocol.AgentView) error {
	if len(rows) == 0 {
		return nil
	}
	parents, err := agentParentsQ(q)
	if err != nil {
		return err
	}
	g, err := loadPlaceGraph(q, parents)
	if err != nil {
		return err
	}
	for i := range rows {
		if rows[i].AgentID != "" {
			rows[i].ReportsTo = parents[rows[i].AgentID]
		}
		rows[i].Parent, rows[i].NoParent = sessionParent(placeOf(rows[i]), g)
	}
	return nil
}
