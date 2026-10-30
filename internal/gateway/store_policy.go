package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Delegation policy storage (decision 0014). A session has its own policy
// (sessions.delegation) and its project has a default (project_policies, keyed
// by machine and lower-case project name). The session's own value wins.

// projectKey is the project a session belongs to, as the policy table names it.
func projectKey(a protocol.AgentView) string {
	p := a.Repo.Project
	if p == "" {
		p = a.Profile.Project
	}
	return strings.ToLower(strings.TrimSpace(p))
}

func encodePolicy(p protocol.DelegationPolicy) string {
	if p.Empty() {
		return ""
	}
	b, _ := json.Marshal(p)
	return string(b)
}

// SetDelegation stores the policy of a session (ScopeSession) or the default
// of its project (ScopeProject, which also drops the session's own policy so
// the session follows the project). An empty policy clears it at that scope.
// It returns false when the session does not exist or is the owner.
func (s *Store) SetDelegation(sessionID, scope string, p protocol.DelegationPolicy) (bool, error) {
	a, ok, err := s.Agent(sessionID)
	if err != nil || !ok || protocol.IsOwner(a.Harness) {
		return false, err
	}
	switch scope {
	case protocol.ScopeSession:
		_, err = s.db.Exec(`UPDATE sessions SET delegation = ? WHERE id = ?`, encodePolicy(p), sessionID)
	case protocol.ScopeProject:
		key := projectKey(a)
		if key == "" {
			return false, errors.New("this session has no project: set the policy for the session instead")
		}
		if p.Empty() {
			_, err = s.db.Exec(`DELETE FROM project_policies WHERE node_id = ? AND project = ?`, a.NodeID, key)
		} else {
			_, err = s.db.Exec(`INSERT INTO project_policies (node_id, project, policy, updated_at) VALUES (?,?,?,?)
				ON CONFLICT (node_id, project) DO UPDATE SET policy = excluded.policy, updated_at = excluded.updated_at`,
				a.NodeID, key, encodePolicy(p), s.now().UnixNano())
		}
		if err == nil {
			_, err = s.db.Exec(`UPDATE sessions SET delegation = '' WHERE id = ?`, sessionID)
		}
	default:
		return false, errors.New("scope must be session or project")
	}
	return err == nil, err
}

// WorkerPermissions is the permission level of workers started in a project
// on a machine (decision 0020): the owner's setting in the project policy,
// else the node's worker sandbox default from its maturity (decision 0022).
// Only the project policy counts: a session's own policy has no workers level.
func (s *Store) WorkerPermissions(nodeID, project string) (string, error) {
	var raw string
	err := s.db.QueryRow(`SELECT policy FROM project_policies WHERE node_id = ? AND project = ?`,
		nodeID, strings.ToLower(strings.TrimSpace(project))).Scan(&raw)
	if err == nil {
		var p protocol.DelegationPolicy
		if json.Unmarshal([]byte(raw), &p) == nil && protocol.ValidWorkerPermissions(p.Workers) && p.Workers != "" {
			return p.Workers, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return s.nodeWorkerSandbox(project)
}

// nodeWorkerSandbox is the sandbox level a node's maturity gives its workers,
// after the node's own override. A node with no maturity keeps today's default.
func (s *Store) nodeWorkerSandbox(project string) (string, error) {
	var maturity string
	err := s.db.QueryRow(`SELECT value FROM graph_charter WHERE node = ? AND attr = 'maturity'`, project).Scan(&maturity)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.WorkersSandboxed, nil
	}
	if err != nil {
		return "", err
	}
	settings := map[string]string{}
	rows, err := s.db.Query(`SELECT setting,value FROM graph_node_settings WHERE node = ?`, project)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var setting, value string
		if err := rows.Scan(&setting, &value); err != nil {
			rows.Close()
			return "", err
		}
		settings[setting] = value
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	return protocol.NodeDefaults(maturity, settings).WorkerSandbox, nil
}

// projectPolicies loads every project default, keyed by node and project.
func (s *Store) projectPolicies() (map[string]protocol.DelegationPolicy, error) {
	rows, err := s.db.Query(`SELECT node_id, project, policy FROM project_policies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]protocol.DelegationPolicy{}
	for rows.Next() {
		var node, project, raw string
		if err := rows.Scan(&node, &project, &raw); err != nil {
			return nil, err
		}
		var p protocol.DelegationPolicy
		if json.Unmarshal([]byte(raw), &p) == nil && !p.Empty() {
			out[node+"\x00"+project] = p
		}
	}
	return out, rows.Err()
}

// applyDelegation fills Delegation of each agent: its own policy, else its
// project's default, with the models resolved against its machine. It also
// clears the policy of an owner pseudo-session.
func (s *Store) applyDelegation(as []protocol.AgentView) error {
	pp, err := s.projectPolicies()
	if err != nil {
		return err
	}
	var nodes map[string]protocol.NodeView
	usage := map[string]map[string]int{}
	for i := range as {
		a := &as[i]
		if protocol.IsOwner(a.Harness) {
			a.Delegation = nil
			continue
		}
		if a.Delegation == nil {
			if key := projectKey(*a); key != "" {
				if p, ok := pp[a.NodeID+"\x00"+key]; ok {
					a.Delegation = &protocol.DelegationView{DelegationPolicy: p, Scope: protocol.ScopeProject}
				}
			}
		}
		if a.Delegation == nil {
			continue
		}
		// The project's workers level is on every view, so a form for a
		// session with its own policy still starts from the project's level.
		if key := projectKey(*a); key != "" {
			if p, ok := pp[a.NodeID+"\x00"+key]; ok {
				a.Delegation.ProjectWorkers = p.Workers
			}
		}
		if nodes == nil {
			list, err := s.Nodes()
			if err != nil {
				return err
			}
			nodes = map[string]protocol.NodeView{}
			for _, n := range list {
				nodes[n.NodeID] = n
			}
		}
		var hi *protocol.HarnessInfo
		for j, h := range nodes[a.NodeID].Harnesses {
			if h.Name == a.Harness {
				hi = &nodes[a.NodeID].Harnesses[j]
			}
		}
		ukey := a.NodeID + "\x00" + a.Harness
		resolve := func(asked string) (string, string, bool) {
			if asked == "" || hi == nil || len(hi.Models) == 0 {
				return "", "", false
			}
			u, ok := usage[ukey]
			if !ok {
				u, err = s.ModelUsage(a.NodeID, a.Harness, time.Duration(protocol.ModelUsageDays)*24*time.Hour)
				if err != nil {
					u = nil
				}
				usage[ukey] = u
			}
			fa, _ := s.FailedModels(a.NodeID, a.Harness)
			sa, _ := s.SucceededModels(a.NodeID, a.Harness)
			r, err := protocol.ResolveModelRanked(asked, hi.Models, u, hi.ModelProviders, failedSet(fa), failedSet(sa))
			if err != nil {
				return "", err.Error(), true
			}
			return r.Model, "", true
		}
		d := a.Delegation
		var listed1, listed2 bool
		d.CoderResolved, d.CoderError, listed1 = resolve(d.CoderModel)
		d.MainResolved, d.MainError, listed2 = resolve(d.MainModel)
		d.ModelList = listed1 || listed2 || (hi != nil && len(hi.Models) > 0)
	}
	return nil
}

// PolicyDryRun works out the effective policy a session would have if p were
// saved at scope, without saving it. It is the same resolver as a read, so the
// form shows what a save will do. The view is nil when the session would end
// up with no policy. ok is false for an unknown session or the owner.
func (s *Store) PolicyDryRun(sessionID, scope string, p protocol.DelegationPolicy) (*protocol.DelegationView, bool, error) {
	a, ok, err := s.Agent(sessionID)
	if err != nil || !ok || protocol.IsOwner(a.Harness) {
		return nil, false, err
	}
	switch {
	case !p.Empty():
		a.Delegation = &protocol.DelegationView{DelegationPolicy: p, Scope: scope}
	case scope == protocol.ScopeSession:
		a.Delegation = nil // the project default, if any, shows through
	case a.Delegation != nil && a.Delegation.Scope == protocol.ScopeProject:
		a.Delegation = nil // clearing the project default; the session has none of its own
	}
	one := []protocol.AgentView{a}
	if a.Delegation == nil && scope == protocol.ScopeSession {
		// applyDelegation finds the project default for a view that has none.
		if err := s.applyDelegation(one); err != nil {
			return nil, false, err
		}
		return one[0].Delegation, true, nil
	}
	if a.Delegation != nil {
		if err := s.applyDelegation(one); err != nil {
			return nil, false, err
		}
	}
	return one[0].Delegation, true, nil
}
