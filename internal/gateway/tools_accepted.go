package gateway

import "time"

// ToolsAccepted lists the keys of the differences the owner accepted as they
// are. The mark is durable: it stays across gateway restarts.
func (s *Store) ToolsAccepted() ([]string, error) {
	rows, err := s.db.Query(`SELECT key FROM tool_accepted ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []string{}
	for rows.Next() {
		var k string
		if err = rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// SetToolAccepted adds or removes one accepted mark.
func (s *Store) SetToolAccepted(key string, accepted bool) error {
	if accepted {
		_, err := s.db.Exec(`INSERT OR REPLACE INTO tool_accepted(key,at) VALUES(?,?)`, key, time.Now().Unix())
		return err
	}
	_, err := s.db.Exec(`DELETE FROM tool_accepted WHERE key=?`, key)
	return err
}
