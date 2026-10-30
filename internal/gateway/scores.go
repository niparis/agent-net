package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

const scoresKey = "model_scores"

func (s *Store) scores() ([]protocol.ModelScore, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, scoresKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return []protocol.ModelScore{}, nil
	}
	if err != nil {
		return nil, err
	}
	var scores []protocol.ModelScore
	if err := json.Unmarshal([]byte(raw), &scores); err != nil {
		return nil, err
	}
	return scores, nil
}

func (g *Gateway) handleScoresGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.nodeAuthed(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	scores, err := g.store.scores()
	writeJSON(w, scores, err)
}

func (g *Gateway) handleScoresSet(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var q protocol.ScoreSetRequest
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&q) != nil || strings.TrimSpace(q.Model) != q.Model || q.Model == "" || len(q.Model) > 256 || strings.IndexFunc(q.Model, unicode.IsControl) >= 0 || q.Score != nil && (*q.Score < 0 || *q.Score > 100) {
		http.Error(w, "bad score", http.StatusBadRequest)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	scores, err := g.store.scores()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out := make([]protocol.ModelScore, 0, len(scores)+1)
	for _, s := range scores {
		if !strings.EqualFold(s.Match, q.Model) {
			out = append(out, s)
		}
	}
	if q.Score != nil {
		out = append(out, protocol.ModelScore{Match: q.Model, Score: *q.Score, Source: "owner"})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	_, err = g.store.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, scoresKey, string(raw), g.store.now().UnixNano())
	if err == nil {
		g.store.Event("scores.set", node, "", "", "")
	}
	writeJSON(w, out, err)
}
