package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/niparis/agent-net/internal/protocol"
)

// A model a machine could not run at launch ("Model unavailable", or a
// provider with no login) is kept out of that machine's candidates until its
// daemon reports a new harness inventory. The mark lives in memory: a gateway
// restart is followed by each daemon's hello, which carries its inventory.

// unavailableKey names one model of one harness, without the effort.
func unavailableKey(harness, model string) string {
	return harness + "\x00" + protocol.ParseModelSelection(harness, "", model, "").ModelID()
}

// markModelUnavailable records that a machine could not run a model.
func (s *Store) markModelUnavailable(nodeID, harness, model, failure string) {
	if nodeID == "" || harness == "" || model == "" {
		return
	}
	s.unavailableMu.Lock()
	defer s.unavailableMu.Unlock()
	if s.unavailable == nil {
		s.unavailable = map[string]map[string]string{}
	}
	if s.unavailable[nodeID] == nil {
		s.unavailable[nodeID] = map[string]string{}
	}
	s.unavailable[nodeID][unavailableKey(harness, model)] = failure
}

// modelUnavailable tells whether a machine could not run a model since its
// last harness inventory, and the harness's error.
func (s *Store) modelUnavailable(nodeID, harness, model string) (string, bool) {
	s.unavailableMu.Lock()
	defer s.unavailableMu.Unlock()
	failure, ok := s.unavailable[nodeID][unavailableKey(harness, model)]
	return failure, ok
}

// clearModelsUnavailable forgets a machine's marks: its new harness
// inventory says again what it can run.
func (s *Store) clearModelsUnavailable(nodeID string) {
	s.unavailableMu.Lock()
	defer s.unavailableMu.Unlock()
	delete(s.unavailable, nodeID)
}

// workerModelUnavailable serves "worker.model_unavailable": an automatic start
// failed at launch because the machine could not run its model. The model is
// marked unavailable there, and a new start is tracked for the same task, so
// the machine can queue it and the gate picks the next model.
func (g *Gateway) workerModelUnavailable(nc *nodeConn, p protocol.WorkerModelUnavailable) (protocol.WorkerModelUnavailableAnswer, error) {
	if p.Owner == "" || p.Harness == "" || p.Model == "" {
		return protocol.WorkerModelUnavailableAnswer{}, reject("a model-unavailable report needs an owner, a harness and a model")
	}
	// The tracked start proves the machine was handed this start for this owner.
	if err := g.store.verifyRecheckStart(nc.id, p.Ref, p.Owner); err != nil {
		return protocol.WorkerModelUnavailableAnswer{}, err
	}
	g.store.markModelUnavailable(nc.id, p.Harness, p.Model, p.Failure)
	g.logLine("worker.model_unavailable", "node", nc.id, "host", g.store.hostname(nc.id), "start", p.Ref,
		"harness", p.Harness, "model", p.Model, "failure", p.Failure)
	project, permissions, done, err := g.store.startFacts(p.Ref)
	if err != nil {
		return protocol.WorkerModelUnavailableAnswer{}, err
	}
	ref := protocol.NewID("q")
	if err := g.store.trackWorkerStart(ref, nc.id, p.Owner, project, p.Harness, permissions, done); err != nil {
		return protocol.WorkerModelUnavailableAnswer{}, err
	}
	return protocol.WorkerModelUnavailableAnswer{Ref: ref}, nil
}

// startFacts reads the project, permissions and done list of a tracked start.
func (s *Store) startFacts(ref string) (project, permissions string, done []string, err error) {
	var raw string
	err = s.db.QueryRow(`SELECT project, permissions, done FROM worker_starts WHERE ref=? OR (legacy_ref=? AND legacy_ref<>'')`, ref, ref).Scan(&project, &permissions, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil, reject("start %s is not tracked", ref)
	}
	if err != nil {
		return "", "", nil, err
	}
	// An older start has no done list; the new start then has none either.
	_ = json.Unmarshal([]byte(raw), &done)
	return project, permissions, done, nil
}
