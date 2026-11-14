package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

type resolvedContext struct {
	Project       string
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
	if pending, err := s.SessionAgentPending(session); err != nil {
		return resolvedContext{}, err
	} else if pending {
		return resolvedContext{}, reject("session Agent selection is pending")
	}
	return s.resolveContext(session, "", false)
}

func (s *Store) resolveRunContext(run int64) (resolvedContext, error) {
	var session, callerAgent string
	if err := s.db.QueryRow(`SELECT session_id,COALESCE(caller_agent_id,'') FROM worker_runs WHERE id=?`, run).Scan(&session, &callerAgent); err != nil {
		return resolvedContext{}, err
	}
	return s.resolveContext(session, callerAgent, true)
}

// sessionIdentity is the durable identity and repo context. Project is the
// legacy adapter for sessions reported by old daemons without repo metadata.
// sessionRepoSQL is shared with queries that run inside a transaction, where
// calling the resolver on the store's single database connection would block.
const sessionRepoSQL = `COALESCE(s.repo_id,NULLIF(s.repo_project,''),p.project,'')`

func sessionIdentityQuery(q interface{ QueryRow(string, ...any) *sql.Row }, session string) (project, agent, harness string, worker bool, err error) {
	var w string
	err = q.QueryRow(`SELECT `+sessionRepoSQL+`,COALESCE(s.agent_id,''),s.harness,s.worker FROM sessions s LEFT JOIN profiles p ON p.session_id=s.id WHERE s.id=?`, session).Scan(&project, &agent, &harness, &w)
	return project, agent, harness, w != "", err
}

func (s *Store) sessionIdentity(session string) (project, agent, harness string, worker bool, err error) {
	return sessionIdentityQuery(s.db, session)
}

func (s *Store) resolveContext(session, callerOverride string, isRun bool) (resolvedContext, error) {
	project, agent, harness, worker, err := s.sessionIdentity(session)
	if err != nil {
		return resolvedContext{}, err
	}
	if harness == "cto" {
		project = ""
	}
	c, err := s.resolveRepoContext(project)
	if err != nil {
		return c, err
	}
	c.Project = project
	if protocol.IsOwner(harness) {
		return c, nil
	}
	if worker || isRun {
		c.CallerContext = true
		agent = callerOverride
		if !isRun {
			err = s.db.QueryRow(`SELECT COALESCE(CASE WHEN EXISTS(SELECT 1 FROM worker_starts WHERE session_id=s.id) THEN (SELECT caller_agent_id FROM worker_starts WHERE session_id=s.id ORDER BY created_at DESC,rowid DESC LIMIT 1) ELSE (SELECT agent_id FROM sessions WHERE id=json_extract(CASE WHEN json_valid(s.worker) THEN s.worker ELSE '{}' END,'$.owner')) END,'') FROM sessions s WHERE id=?`, session).Scan(&agent)
			if err != nil {
				return c, err
			}
		}
	}
	c.Agent = agent
	if c.Agent == "" {
		return c, nil
	}

	c.AgentCharter, err = s.EffectiveCharter(c.Repo, c.Agent, "")
	return c, err
}

func (c resolvedContext) agentText() string {
	var b strings.Builder
	for _, a := range []struct{ key, value string }{{"purpose", c.AgentCharter.Purpose}, {"maturity", c.AgentCharter.Maturity}, {"accepts", c.AgentCharter.Accepts}, {"refuses", c.AgentCharter.Refuses}, {"feature_requests", c.AgentCharter.FeatureRequests}, {"escalate", c.AgentCharter.Escalate}, {"maturity_note", c.AgentCharter.MaturityNote}, {"rules", c.AgentCharter.Rules}} {
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

// joinDelegated adds the "Delegated authority" line after the agent charter.
func joinDelegated(charter, block string) string {
	switch {
	case block == "":
		return charter
	case charter == "":
		return block + "\n"
	}
	return charter + block + "\n"
}
