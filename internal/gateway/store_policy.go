package gateway

import (
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

// WorkerPermissions is the permission level of workers started in a project
// (decision 0020): the owner's repo setting workers.sandbox_level, else the
// node's worker sandbox default from its maturity (decision 0022).
func (s *Store) WorkerPermissions(_ string, project string) (string, error) {
	r, err := s.workerPermissionReader()
	if err != nil {
		return "", err
	}
	return r.permissions(project)
}

// agentViewTables are the tables behind agentViewInputs: the settings, the names
// of the graph nodes and the machines. Keep it equal to those queries (see
// readThrough).
var agentViewTables = tableUnion(settingsTables, graphNodesTables, nodesTables)

// permissionReader answers WorkerPermissions for many projects from one read
// of the graph names and the settings.
type permissionReader struct {
	names map[string][]string
	snap  settings.Snapshot
}

func (s *Store) workerPermissionReader() (*permissionReader, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	names, err := workerProjectNamesTx(tx)
	if err != nil {
		return nil, err
	}
	snap, err := settingsSnapshotTx(tx)
	if err != nil {
		return nil, err
	}
	return &permissionReader{names, snap}, nil
}

func (r *permissionReader) permissions(project string) (string, error) {
	key := strings.ToLower(strings.TrimSpace(project))
	matches := r.names[key]
	if len(matches) > 1 {
		return "", reject("ambiguous project %q: graph names differ only by case", key)
	}
	if len(matches) == 1 {
		project = matches[0]
	} else {
		project = key
	}
	return protocol.WorkerPermissionsOrDefault(r.snap.String(workerSandboxKey, settings.Where{Repo: project})), nil
}

// agentViewInputs is the graph and the settings snapshot an agent view is
// built from. A request reads them once and passes them down, so a view does
// not rebuild the graph for each step. Callers must not change them.
type agentViewInputs struct {
	graph protocol.Graph
	snap  settings.Snapshot
}

func (s *Store) agentViewInputs() (agentViewInputs, error) {
	var in agentViewInputs
	var err error
	if in.snap, err = s.SettingsSnapshot(); err != nil {
		return in, err
	}
	in.graph, err = s.Graph()
	return in, err
}
