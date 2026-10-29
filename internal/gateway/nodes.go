package gateway

import "github.com/niparis/agent-net/internal/protocol"

// SetNodeHello records what a daemon said in its hello: host name and version.
func (s *Store) SetNodeHello(nodeID, hostname, version string) error {
	if err := s.SetNodeHostname(nodeID, hostname); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE nodes SET version = ?, seen_at = ? WHERE id = ?`, version, s.now().UnixNano(), nodeID)
	return err
}

// NodeVersion returns the version a node last reported.
func (s *Store) NodeVersion(nodeID string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT version FROM nodes WHERE id = ?`, nodeID).Scan(&v)
	return v, err
}

// Nodes lists every node that has connected at least once. Online is left
// false; the gateway fills it from its live connections.
func (s *Store) Nodes() ([]protocol.NodeView, error) {
	rows, err := s.db.Query(`SELECT id, hostname, os, version, seen_at FROM nodes WHERE seen_at > 0 ORDER BY hostname, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.NodeView
	for rows.Next() {
		var n protocol.NodeView
		var seen int64
		if err := rows.Scan(&n.NodeID, &n.Hostname, &n.OS, &n.Version, &seen); err != nil {
			return nil, err
		}
		n.LastSeen = seen / 1e9
		out = append(out, n)
	}
	return out, rows.Err()
}
