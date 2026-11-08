package gateway

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"

	"github.com/niparis/agent-net/internal/protocol"
)

// qualityPreviewInputs holds what every node's preview reads from the store.
// NodesInfo reads it once per call instead of once per node.
type qualityPreviewInputs struct {
	profiles  []protocol.WorkerProfile
	providers protocol.WorkerProviders
	protected protocol.ProtectedProviders
	scores    []protocol.ModelScore
	minimums  map[string]float64
	// running counts the online workers by quota provider, per node.
	running map[string]map[string]int
	// health answers model health by harness and model. It does not depend on
	// the node or the difficulty level, so one read serves the whole call.
	health *healthReader
}

func (g *Gateway) qualityPreviewInputs() (qualityPreviewInputs, error) {
	in := qualityPreviewInputs{health: g.store.newHealthReader()}
	var err error
	if in.profiles, err = g.store.WorkerProfiles(); err != nil {
		return in, err
	}
	if in.providers, err = g.store.workerProviders(); err != nil {
		return in, err
	}
	if in.protected, err = g.store.protectedProviders(); err != nil {
		return in, err
	}
	if in.scores, err = g.store.scores(); err != nil {
		return in, err
	}
	if in.minimums, err = g.store.difficultyMinimums(); err != nil {
		return in, err
	}
	if in.running, err = g.store.onlineWorkerCounts(); err != nil {
		return in, err
	}
	return in, nil
}

// qualityPreviewWith previews the pick for every difficulty level. The reads
// that do not depend on the level are made once: model health for the whole
// call, the quality settings and the rotation for the node.
//
// The picks are a function of the roster, the scores, the minimums, the
// quotas, the running counts, the protected providers, the rotation and the
// health of each model. Their values are hashed, and a node whose hash is
// unchanged since the last call gets the picks of that call. A changed
// profile, score, quota, rotation, track record or health changes the hash on
// the next call, so a stored answer is never older than its inputs.
func (g *Gateway) qualityPreviewWith(in qualityPreviewInputs, node protocol.NodeView, facts *candidateFacts) (map[string]string, map[string]string, map[string]string, error) {
	if !node.Online || node.Launch == nil || !*node.Launch {
		return nil, nil, nil, nil
	}
	running := in.running[node.NodeID]
	if running == nil {
		running = map[string]int{}
	}
	candidates, _, _, err := g.workerModelCandidatesWith(facts, &node, in.profiles, in.providers, node.Quotas, "", false, in.protected)
	if err != nil {
		return nil, nil, nil, err
	}
	rotation, rotationErr := g.store.WorkerRotation()
	readRotation := func() (protocol.WorkerRotation, error) { return cloneRotation(rotation), rotationErr }
	health := in.health.health

	// The cell table is read once per call. A level previews at the middle of its
	// D range. The table revision is part of the key below.
	table := g.store.pickerTable()
	key, keyed := g.previewKey(node.NodeID, in, node.Quotas, running, candidates, table.Revision, rotation, rotationErr, health)
	if keyed {
		if out, reasons, models, ok := g.previewMemo.get(node.NodeID, key); ok {
			return out, reasons, models, nil
		}
	}

	out := map[string]string{}
	reasons := map[string]string{}
	models := map[string]string{}
	for _, level := range protocol.DifficultyLevels {
		pick := &protocol.PickerInput{D: protocol.PickerD(nil, level), Table: table}
		choice, _, err := g.chooseAutomaticWorkerCore(health, readRotation, pick, candidates, level, in.scores, in.minimums, node.Quotas, running, in.protected, false)
		if err == nil {
			out[level] = qualityModelFamily(choice.Model)
			models[level] = qualityModelKey(choice.Model)
			if reason := qualityPaceClause(choice.QuotaNote); reason != "" {
				reasons[level] = reason
			}
		}
	}
	if keyed {
		g.previewMemo.put(node.NodeID, key, out, reasons, models)
	}
	return out, reasons, models, nil
}

func cloneRotation(r protocol.WorkerRotation) protocol.WorkerRotation {
	if r.Levels == nil {
		return r
	}
	out := protocol.WorkerRotation{Levels: make(map[string]*protocol.WorkerRotationLevel, len(r.Levels))}
	for k, v := range r.Levels {
		if v == nil {
			out.Levels[k] = nil
			continue
		}
		c := &protocol.WorkerRotationLevel{Recent: append([]string(nil), v.Recent...)}
		if v.Current != nil {
			c.Current = make(map[string]float64, len(v.Current))
			for name, w := range v.Current {
				c.Current[name] = w
			}
		}
		out.Levels[k] = c
	}
	return out
}

// previewKey hashes everything the picks of one node are computed from. It
// reports false when an input cannot be hashed, and the caller then computes
// without the stored answer.
func (g *Gateway) previewKey(node string, in qualityPreviewInputs, quotas *protocol.Quotas, running map[string]int, candidates []protocol.WorkerModelCandidate, pickerRevision uint64, rotation protocol.WorkerRotation, rotationErr error, health func(harness, model string) (ModelHealth, error)) ([32]byte, bool) {
	if rotationErr != nil {
		return [32]byte{}, false
	}
	var healths []ModelHealth
	for _, c := range candidates {
		h, err := health(c.Harness, c.Model)
		if err != nil {
			return [32]byte{}, false
		}
		healths = append(healths, h)
	}
	raw, err := json.Marshal(struct {
		Node       string
		Candidates []protocol.WorkerModelCandidate
		Picker     uint64
		Healths    []ModelHealth
		Scores     []protocol.ModelScore
		Minimums   map[string]float64
		Quotas     *protocol.Quotas
		Running    map[string]int
		Protected  protocol.ProtectedProviders
		Rotation   protocol.WorkerRotation
	}{node, candidates, pickerRevision, healths, in.scores, in.minimums, quotas, running, in.protected, rotation})
	if err != nil {
		return [32]byte{}, false
	}
	return sha256.Sum256(raw), true
}

// previewMemo keeps the last picks of each node with the hash of their inputs.
type previewMemo struct {
	mu sync.Mutex
	m  map[string]previewEntry
	// hits counts the answers served from the stored picks.
	hits int
}

type previewEntry struct {
	key                    [32]byte
	picks, reasons, models map[string]string
}

func copyStrings(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (p *previewMemo) get(node string, key [32]byte) (picks, reasons, models map[string]string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, found := p.m[node]
	if !found || e.key != key {
		return nil, nil, nil, false
	}
	p.hits++
	return copyStrings(e.picks), copyStrings(e.reasons), copyStrings(e.models), true
}

func (p *previewMemo) put(node string, key [32]byte, picks, reasons, models map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]previewEntry{}
	}
	p.m[node] = previewEntry{key, copyStrings(picks), copyStrings(reasons), copyStrings(models)}
}

// qualityPaceClause preserves the chooser's concise pace description.
func qualityPaceClause(note string) string {
	clause, _, _ := strings.Cut(note, " (")
	if strings.Contains(clause, " ahead of pace") || strings.Contains(clause, " behind pace") {
		return clause
	}
	return ""
}

// onlineWorkerCounts counts the online workers by node and quota provider. It
// reads the same sessions as Agents(OnlineOnly) with a worker, without the
// delegation and charter work a full agent view needs.
func (s *Store) onlineWorkerCounts() (map[string]map[string]int, error) {
	rows, err := s.db.Query(`SELECT s.node_id, s.harness, s.model, s.worker
		FROM sessions s LEFT JOIN nodes n ON n.id = s.node_id
		WHERE s.archived_at=0 AND n.id IS NOT NULL AND s.online=1 AND s.worker<>'' AND s.harness NOT IN ('bot','owner')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var node, harness, model, worker string
		if err := rows.Scan(&node, &harness, &model, &worker); err != nil {
			return nil, err
		}
		var w protocol.WorkerInfo
		if json.Unmarshal([]byte(worker), &w) != nil {
			continue
		}
		if out[node] == nil {
			out[node] = map[string]int{}
		}
		out[node][protocol.QuotaProviderForStart(harness, model)]++
	}
	return out, rows.Err()
}
