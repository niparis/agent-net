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

const difficultyKey = "difficulty_minimums"

func (s *Store) difficultyMinimums() (map[string]float64, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, difficultyKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]float64{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string]float64
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}
func (g *Gateway) handleDifficulty(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet {
		out, err := g.store.difficultyMinimums()
		writeJSON(w, out, err)
		return
	}
	var in map[string]float64
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || in == nil {
		http.Error(w, "bad difficulty settings", 400)
		return
	}
	for k, v := range in {
		if _, ok := protocol.DifficultyMinimums[k]; !ok || v < 0 || v > 100 {
			http.Error(w, "bad difficulty settings", 400)
			return
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	_, err = g.store.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, difficultyKey, string(raw), g.store.now().UnixNano())
	if err == nil {
		g.store.Event("difficulty.set", node, "", "", "")
	}
	writeJSON(w, in, err)
}

const workerProvidersKey = "worker_providers"

func (s *Store) workerProviders() (protocol.WorkerProviders, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, workerProvidersKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.WorkerProviders{}, nil
	}
	if err != nil {
		return protocol.WorkerProviders{}, err
	}
	var out protocol.WorkerProviders
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}
func (g *Gateway) handleWorkerProviders(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet {
		out, err := g.store.workerProviders()
		writeJSON(w, out, err)
		return
	}
	var in protocol.WorkerProviders
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in) != nil || len(in.Providers) > 128 {
		http.Error(w, "bad providers", 400)
		return
	}
	seen := map[string]bool{}
	for i, p := range in.Providers {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || len(p) > 64 || strings.IndexFunc(p, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
		}) >= 0 || seen[p] {
			http.Error(w, "bad provider", 400)
			return
		}
		seen[p] = true
		in.Providers[i] = p
	}
	raw, err := json.Marshal(in)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	_, err = g.store.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, workerProvidersKey, string(raw), g.store.now().UnixNano())
	if err == nil {
		g.store.Event("worker_providers.set", node, "", "", "")
	}
	writeJSON(w, in, err)
}

const protectedProvidersKey = "protected_providers"

func (s *Store) protectedProviders() (protocol.ProtectedProviders, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM owner_settings WHERE key = ?`, protectedProvidersKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.ProtectedProviders{}, nil
	}
	if err != nil {
		return protocol.ProtectedProviders{}, err
	}
	var out protocol.ProtectedProviders
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}

func (g *Gateway) handleProtectedProviders(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet {
		out, err := g.store.protectedProviders()
		writeJSON(w, out, err)
		return
	}
	var in protocol.ProtectedProviders
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil || len(in.Providers) > 128 || len(in.Reason) > 4096 {
		http.Error(w, "bad protected providers", 400)
		return
	}
	seen := map[string]bool{}
	for i, p := range in.Providers {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || len(p) > 64 || strings.IndexFunc(p, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
		}) >= 0 || seen[p] {
			http.Error(w, "bad provider", 400)
			return
		}
		seen[p] = true
		in.Providers[i] = p
	}
	raw, err := json.Marshal(in)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	_, err = g.store.db.Exec(`INSERT INTO owner_settings (key,value,updated_at) VALUES (?,?,?) ON CONFLICT (key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, protectedProvidersKey, string(raw), g.store.now().UnixNano())
	if err == nil {
		g.store.Event("protected_providers.set", node, "", "", "")
	}
	writeJSON(w, in, err)
}
