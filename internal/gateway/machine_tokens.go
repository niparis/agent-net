package gateway

import (
	"context"
	"encoding/json"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func (s *Store) machineRemoved(id string) bool {
	var removed int
	_ = s.db.QueryRow(`SELECT removed FROM nodes WHERE id=?`, id).Scan(&removed)
	return removed != 0
}

func (s *Store) stageNodeToken(id, token string) error {
	res, err := s.db.Exec(`UPDATE nodes SET pending_token_hash=? WHERE id=? AND removed=0`, hashHex(token), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return reject("unknown or removed machine")
	}
	return nil
}

func (s *Store) confirmNodeToken(id, token string) error {
	hash := hashHex(token)
	res, err := s.db.Exec(`UPDATE nodes SET token_hash=?,pending_token_hash='' WHERE id=? AND removed=0 AND (pending_token_hash=? OR token_hash=?)`, hash, id, hash, hash)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return reject("machine token confirmation failed")
	}
	return nil
}

func (s *Store) removeMachine(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// token_hash is unique and not nullable. A public tombstone deletes the
	// credential without rebuilding the table or losing its foreign keys.
	res, err := tx.Exec(`UPDATE nodes SET removed=1,token_hash='removed:'||id,pending_token_hash='' WHERE id=? AND removed=0`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return reject("unknown or removed machine")
	}
	if _, err = tx.Exec(`UPDATE sessions SET online=0 WHERE node_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (g *Gateway) checkMachineAction(id string, rotate bool) error {
	var removed int
	if id == "" {
		return reject("machine action needs node_id")
	}
	if err := g.store.db.QueryRow(`SELECT removed FROM nodes WHERE id=?`, id).Scan(&removed); err != nil || removed != 0 {
		return reject("unknown or removed machine")
	}
	if rotate && g.conn(id) == nil {
		return reject("machine is offline; offline machines can only be removed, not rotated")
	}
	return nil
}

func (g *Gateway) machineAction(ctx context.Context, nc *nodeConn, typ string, raw []byte) (any, error) {
	var p struct {
		NodeID string `json:"node_id"`
		Owner  string `json:"owner"`
		Actor  string `json:"actor"`
	}
	if json.Unmarshal(raw, &p) != nil || !g.validToolOwner(nc, p.Owner) || !validChangeActor(p.Actor) || p.Actor == "cto" {
		return nil, reject("owner authority required")
	}
	rotate := typ == "machine.rotate"
	if !rotate && p.NodeID == nc.id {
		return nil, reject("a machine cannot remove itself; remove it from another machine")
	}
	if err := g.checkMachineAction(p.NodeID, rotate); err != nil {
		return nil, err
	}
	if !rotate {
		g.machineAuthMu.Lock()
		err := g.store.removeMachine(p.NodeID)
		g.mu.Lock()
		target := g.conns[p.NodeID]
		if err == nil {
			delete(g.conns, p.NodeID)
			if t := g.timers[p.NodeID]; t != nil {
				t.timer.Stop()
				delete(g.timers, p.NodeID)
			}
		}
		g.mu.Unlock()
		g.machineAuthMu.Unlock()
		if err != nil {
			return nil, err
		}
		if target != nil && target != nc {
			target.cancel()
			target.ws.CloseNow()
		}
		g.store.Event(typ, nc.id, "", "", p.NodeID+" by "+p.Actor)
		return map[string]bool{"ok": true, "reconnect": target == nc}, nil
	} else {
		g.machineRotationMu.Lock()
		defer g.machineRotationMu.Unlock()
		target := g.conn(p.NodeID)
		if target == nil {
			return nil, reject("machine is offline; offline machines can only be removed, not rotated")
		}
		token := protocol.NewToken()
		if err := g.store.stageNodeToken(p.NodeID, token); err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		result, err := target.call(callCtx, "node.token", map[string]string{"token": token})
		var confirmed struct {
			Saved bool `json:"saved"`
		}
		if err != nil || json.Unmarshal(result, &confirmed) != nil || !confirmed.Saved {
			return nil, reject("rotation was not confirmed; retry when the machine is online")
		}
		// Serialize confirmation with authentication and connection replacement.
		// Close whichever connection still uses the old credential, even if it
		// replaced the original socket while the acknowledgement was in flight.
		g.machineAuthMu.Lock()
		err = g.store.confirmNodeToken(p.NodeID, token)
		current := g.conn(p.NodeID)
		if current != nil && current.tokenHash == hashHex(token) {
			current = nil
		}
		g.machineAuthMu.Unlock()
		if err != nil {
			return nil, err
		}
		g.store.Event(typ, nc.id, "", "", p.NodeID+" by "+p.Actor)
		if current != nil && current != nc {
			current.cancel()
			current.ws.CloseNow()
		}
		if target != nc && target != current {
			target.cancel()
			target.ws.CloseNow()
		}
		// The owner gets its result before its own connection is closed.
		return map[string]bool{"ok": true, "reconnect": current == nc}, nil
	}
}
