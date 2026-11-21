package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/niparis/agent-net/internal/protocol"
)

const workerRotationKey = "worker_rotation"

// WorkerRotation reads the rotating worker model picks. It is gateway state,
// so a restart keeps the spread across starts.
func (s *Store) WorkerRotation() (protocol.WorkerRotation, error) {
	var out protocol.WorkerRotation
	_, err := stateJSON(s.db, workerRotationKey, &out)
	return out, err
}

func (s *Store) saveWorkerRotation(rotation protocol.WorkerRotation) error {
	raw, err := json.Marshal(rotation)
	if err != nil {
		return err
	}
	return stateSet(s.db, workerRotationKey, string(raw), s.now().UnixNano())
}

func (g *Gateway) handleWorkerRotation(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.nodeAuthed(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rotation, err := g.store.WorkerRotation()
	writeJSON(w, rotation, err)
}
