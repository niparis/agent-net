package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

const scoresKey = "owner.model_scores"

func (s *Store) scores() ([]protocol.ModelScore, error) {
	scores := []protocol.ModelScore{}
	_, err := ownerJSON(s.db, scoresKey, &scores)
	return scores, err
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
		out = append(out, protocol.ModelScore{Match: q.Model, Score: *q.Score, Source: "owner", SetAt: g.store.now().UnixNano()})
	}
	err = g.store.setOwner(scoresKey, out)
	if err == nil {
		err = g.store.refreshQuality()
		g.store.Event("scores.set", node, "", "", "")
	}
	writeJSON(w, out, err)
}

const difficultyKey = "owner.difficulty_minimums"

func (s *Store) difficultyMinimums() (map[string]float64, error) {
	out := map[string]float64{}
	_, err := ownerJSON(s.db, difficultyKey, &out)
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
	err := g.store.setOwner(difficultyKey, in)
	if err == nil {
		g.store.Event("difficulty.set", node, "", "", "")
	}
	writeJSON(w, in, err)
}

const workerProvidersKey = "owner.worker_providers"

func (s *Store) workerProviders() (out protocol.WorkerProviders, err error) {
	_, err = ownerJSON(s.db, workerProvidersKey, &out)
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
	err := g.store.setOwner(workerProvidersKey, in)
	if err == nil {
		g.store.Event("worker_providers.set", node, "", "", "")
	}
	writeJSON(w, in, err)
}

const protectedProvidersKey = "owner.protected_providers"

func (s *Store) protectedProviders() (out protocol.ProtectedProviders, err error) {
	out.Configured, err = ownerJSON(s.db, protectedProvidersKey, &out)
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
	in.Configured = true
	err := g.store.setOwner(protectedProvidersKey, in)
	if err == nil {
		g.store.Event("protected_providers.set", node, "", "", "")
	}
	writeJSON(w, in, err)
}

const workerFloorsKey = "owner.worker_floors"

func (s *Store) workerFloors() (out protocol.WorkerFloors, err error) {
	out.Configured, err = ownerJSON(s.db, workerFloorsKey, &out)
	return out, err
}

func (g *Gateway) handleWorkerFloors(w http.ResponseWriter, r *http.Request) {
	node, ok := g.nodeAuthed(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method == http.MethodGet {
		out, err := g.store.workerFloors()
		writeJSON(w, out, err)
		return
	}
	var in protocol.WorkerFloors
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil || len(in.Floors) > 128 {
		http.Error(w, "bad worker floors", 400)
		return
	}
	cleaned := make(map[string]int, len(in.Floors))
	for p, f := range in.Floors {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" || len(p) > 64 || strings.IndexFunc(p, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
		}) >= 0 {
			http.Error(w, "bad provider", 400)
			return
		}
		if f < 0 || f > 95 {
			http.Error(w, "bad floor: must be 0-95", 400)
			return
		}
		if f > 0 {
			cleaned[p] = f
		}
	}
	in.Floors = cleaned
	in.Configured = true
	err := g.store.setOwner(workerFloorsKey, in)
	if err == nil {
		g.store.Event("worker_floors.set", node, "", "", "")
	}
	writeJSON(w, in, err)
}
