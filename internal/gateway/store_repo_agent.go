package gateway

// RepoRecord and AgentRecord expose the additive identities without changing Graph.
type RepoRecord struct {
	ID, DisplayName      string
	OwnerAgentID         *string
	CreatedAt, UpdatedAt int64
	Archived             bool
}

type AgentRecord struct {
	ID, DisplayName, Role string
	CreatedAt, UpdatedAt  int64
	Tombstone             bool
}

// RepoAgents returns all repos and agents, including tombstones.
func (s *Store) RepoAgents() ([]RepoRecord, []AgentRecord, error) {
	rows, err := s.db.Query(`SELECT id,display_name,owner_agent_id,created_at,updated_at,EXISTS(SELECT 1 FROM archived_repos WHERE repo_id=repos.id) FROM repos ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	var repos []RepoRecord
	for rows.Next() {
		var r RepoRecord
		if err = rows.Scan(&r.ID, &r.DisplayName, &r.OwnerAgentID, &r.CreatedAt, &r.UpdatedAt, &r.Archived); err != nil {
			rows.Close()
			return nil, nil, err
		}
		repos = append(repos, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.db.Query(`SELECT id,display_name,role,created_at,updated_at,tombstone FROM agents ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var agents []AgentRecord
	for rows.Next() {
		var a AgentRecord
		if err = rows.Scan(&a.ID, &a.DisplayName, &a.Role, &a.CreatedAt, &a.UpdatedAt, &a.Tombstone); err != nil {
			return nil, nil, err
		}
		agents = append(agents, a)
	}
	return repos, agents, rows.Err()
}
