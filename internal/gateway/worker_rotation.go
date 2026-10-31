package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/niparis/agent-net/internal/protocol"
)

const workerRotationKey = "worker_rotation"

// WorkerRotation reads the rotating worker model picks. It is gateway state,
// so a restart keeps the spread across starts.
func (s *Store) WorkerRotation() (protocol.WorkerRotation, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, workerRotationKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.WorkerRotation{}, nil
	}
	if err != nil {
		return protocol.WorkerRotation{}, err
	}
	var out protocol.WorkerRotation
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

func (s *Store) saveWorkerRotation(rotation protocol.WorkerRotation) error {
	raw, err := json.Marshal(rotation)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, workerRotationKey, string(raw), s.now().UnixNano())
	return err
}

func (g *Gateway) handleWorkerRotation(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.nodeAuthed(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rotation, err := g.store.WorkerRotation()
	writeJSON(w, rotation, err)
}
