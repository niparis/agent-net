package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

type resolvedContext struct {
	Repo          string
	Agent         string
	RepoCharter   protocol.Charter
	AgentCharter  protocol.Charter
	CallerContext bool
	Graph         protocol.Graph
	Settings      settings.Snapshot
}

func (c resolvedContext) resolveSetting(key string, session, run map[string]json.RawMessage) (settings.Effective, error) {
	return c.Settings.Resolve(key, settings.Where{Repo: c.Repo, Agent: c.Agent, Session: session, Run: run})
}

// resolveRepoContext retains the project-keyed delivery compatibility path.
func (s *Store) resolveRepoContext(project string) (resolvedContext, error) {
	var c resolvedContext
	var err error
	c.Graph, err = s.Graph()
	if err != nil {
		return c, err
	}
	c.Settings, err = s.SettingsSnapshot()
	if err != nil {
		return c, err
	}
	c.Repo = chartedProject(c.Graph, project)
	for _, node := range c.Graph.Nodes {
		if node.Name == c.Repo {
			c.RepoCharter = node.Charter
			break
		}
	}
	return c, nil
}

// resolveSessionContext resolves a lasting session or worker using its caller.
func (s *Store) resolveSessionContext(session string) (resolvedContext, error) {
	return s.resolveContext(session, "", false)
}

func (s *Store) resolveRunContext(run int64) (resolvedContext, error) {
	var session, caller string
	if err := s.db.QueryRow(`SELECT session_id,caller_session_id FROM worker_runs WHERE id=?`, run).Scan(&session, &caller); err != nil {
		return resolvedContext{}, err
	}
	return s.resolveContext(session, caller, true)
}

func (s *Store) resolveContext(session, callerOverride string, isRun bool) (resolvedContext, error) {
	a, err := scanAgent(s.db.QueryRow(agentSelect+` WHERE s.id = ? AND (n.id IS NOT NULL OR s.harness='bot')`, session))
	if err != nil {
		return resolvedContext{}, err
	}
	c, err := s.resolveRepoContext(projectOfSession(a))
	if err != nil {
		return c, err
	}
	if a.Harness == "cto" || protocol.IsOwner(a.Harness) {
		return c, nil
	}
	agentRepo := projectOfSession(a)
	if a.Worker != nil || isRun {
		c.CallerContext = true
		callerID := callerOverride
		if callerID == "" && a.Worker != nil {
			callerID = a.Worker.Owner
		}
		caller, err := scanAgent(s.db.QueryRow(agentSelect+` WHERE s.id = ? AND (n.id IS NOT NULL OR s.harness='bot')`, callerID))
		if errors.Is(err, sql.ErrNoRows) {
			return c, nil
		}
		if err != nil {
			return c, err
		}
		if caller.Harness == "cto" || protocol.IsOwner(caller.Harness) {
			return c, nil
		}
		agentRepo = projectOfSession(caller)
	}
	err = s.db.QueryRow(`SELECT COALESCE(owner_agent_id,'') FROM repos WHERE id=?`, agentRepo).Scan(&c.Agent)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	rows, err := s.db.Query(`SELECT attr,value FROM agent_charter WHERE agent_id=? ORDER BY attr`, c.Agent)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return c, err
		}
		switch key {
		case "purpose":
			c.AgentCharter.Purpose = value
		case "maturity":
			c.AgentCharter.Maturity = value
		case "accepts":
			c.AgentCharter.Accepts = value
		case "refuses":
			c.AgentCharter.Refuses = value
		case "feature_requests":
			c.AgentCharter.FeatureRequests = value
		case "consumers":
			c.AgentCharter.Consumers = value
		case "escalate":
			c.AgentCharter.Escalate = value
		case "needs":
			c.AgentCharter.Needs = value
		case "notes":
			c.AgentCharter.Notes = value
		}
	}
	return c, rows.Err()
}

func (c resolvedContext) agentText() string {
	var b strings.Builder
	for _, a := range []struct{ key, value string }{{"purpose", c.AgentCharter.Purpose}, {"maturity", c.AgentCharter.Maturity}, {"accepts", c.AgentCharter.Accepts}, {"refuses", c.AgentCharter.Refuses}, {"feature_requests", c.AgentCharter.FeatureRequests}, {"consumers", c.AgentCharter.Consumers}, {"escalate", c.AgentCharter.Escalate}, {"needs", c.AgentCharter.Needs}, {"notes", c.AgentCharter.Notes}} {
		if a.value != "" {
			fmt.Fprintf(&b, "%s: %s\n", a.key, a.value)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	label := "Your agent charter"
	if c.CallerContext {
		label = "Caller agent charter"
	}
	return "[agent-net] " + label + ":\n" + b.String()
}
