package gateway

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A lost launcher response must not hold new sessions or later selected starts
// indefinitely. Two minutes bounds recovery without making availability a setting.
const sessionAgentStartTimeout = 2 * time.Minute

// agentStart is the owner's Agent choice for a session being started. A
// pending start (native "") holds every new lasting session of its harness on
// the machine, except the existing ones, until its native ID is known. A
// failed start keeps its reason and Desk item.
type agentStart struct {
	node, harness, native, agent, ref, cwd, owner, project string
	existing                                               []string
	expires                                                time.Time
	item, reason                                           string
}

// ReserveSessionAgent validates a choice, prepares a launch hold when Ref is
// present, or binds its exact native ID after the harness creates it. The
// daemon admits this call only through its owner or approved-proposal path.
func (s *Store) ReserveSessionAgent(node string, p protocol.SessionAgentReservation) error {
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return err
	}
	if err := s.requireMachineOwner(node, p.Owner, "session Agent selection requires the owner of this machine"); err != nil {
		return err
	}
	if p.Harness != "claude" && p.Harness != "pi" && p.Harness != "opencode" && p.Harness != "codex" {
		return reject("Agent selection requires a harness with a known native session ID")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if p.Failed {
		if p.Ref == "" {
			return reject("failed start needs its reference")
		}
		if a := s.holds.take(func(a *agentStart) bool { return a.node == node && a.ref == p.Ref }); a != nil {
			a.native = firstNonEmpty(p.NativeID, a.native)
			err = s.failAgentStart(tx, a, "The daemon reported that the start failed")
		} else {
			err = s.noteLateAgentNative(tx, node, p)
		}
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	s.holds.mu.Lock()
	failed := s.holds.failed[[2]string{node, p.Ref}] != nil && p.Ref != ""
	s.holds.mu.Unlock()
	if failed {
		if err = s.noteLateAgentNative(tx, node, p); err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return err
		}
		return reject("Agent choice for %s failed; session uses the Repo-owner default", p.Ref)
	}
	var allowed, registered bool
	var existing string
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0),
		EXISTS(SELECT 1 FROM sessions WHERE node_id=? AND harness=? AND native_id=?),
		(SELECT COALESCE(json_group_array(id),'[]') FROM sessions WHERE node_id=?)`, p.AgentID, node, p.Harness, p.NativeID, node).Scan(&allowed, &registered, &existing); err != nil {
		return err
	}
	if !allowed {
		return reject("unknown or tombstoned Agent %q", p.AgentID)
	}
	a := &agentStart{node: node, harness: p.Harness, native: p.NativeID, agent: p.AgentID, ref: p.Ref, cwd: p.Cwd, owner: p.Owner, project: p.Project,
		expires: s.now().Add(sessionAgentStartTimeout)}
	switch {
	case p.Ref != "" && p.NativeID != "":
		return s.bindStartedAgent(tx, node, p)
	case p.Ref != "" && p.Cwd == "":
		return reject("pending start needs cwd")
	case p.Ref != "":
		_ = json.Unmarshal([]byte(existing), &a.existing)
	case p.NativeID == "":
		return nil
	case registered:
		return reject("session is already registered; its Agent cannot be selected at start")
	default:
		a.ref = p.NativeID
	}
	if !s.holds.add(a) {
		return reject("another %s start is already pending on this machine", p.Harness)
	}
	return nil
}

func (h *startHolds) add(a *agentStart) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := [3]string{a.node, a.harness, a.native}
	if h.agents[key] != nil {
		return false
	}
	if h.agents == nil {
		h.agents = map[[3]string]*agentStart{}
	}
	h.agents[key] = a
	return true
}

// take removes and returns a start that matches.
func (h *startHolds) take(match func(*agentStart) bool) *agentStart {
	h.mu.Lock()
	defer h.mu.Unlock()
	for k, a := range h.agents {
		if match(a) {
			delete(h.agents, k)
			return a
		}
	}
	return nil
}

// heldSQL is the condition on sessions alias that a pending start holds the
// session: a new lasting session of its harness on its machine, not one that
// existed before the launch, and not one whose native ID a failed start
// named. Cwd is not used to guess which concurrent session the launch made.
func (s *Store) heldSQL(alias string) (string, []any) {
	h := &s.holds
	h.mu.Lock()
	defer h.mu.Unlock()
	var parts []string
	var args []any
	for _, a := range h.agents {
		if a.native != "" {
			continue
		}
		failed := []string{}
		for _, f := range h.failed {
			if f.node == a.node && f.harness == a.harness && f.native != "" {
				failed = append(failed, f.native)
			}
		}
		existing, _ := json.Marshal(a.existing)
		natives, _ := json.Marshal(failed)
		parts = append(parts, `(`+alias+`.node_id=? AND `+alias+`.harness=? AND `+alias+`.worker='' AND `+alias+`.id NOT IN (SELECT value FROM json_each(?)) AND `+alias+`.native_id NOT IN (SELECT value FROM json_each(?)))`)
		args = append(args, a.node, a.harness, string(existing), string(natives))
	}
	if len(parts) == 0 {
		return "0", nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func (s *Store) applyStartedAgent(q askDB, node string, in protocol.SessionInfo, id string, known bool) error {
	a := s.holds.take(func(a *agentStart) bool {
		return a.node == node && a.harness == in.Harness && a.native == in.NativeID && in.NativeID != ""
	})
	if a == nil {
		// A pending launch must not temporarily impersonate the Repo's default Agent.
		held, args := s.heldSQL("sessions")
		_, err := q.Exec(`UPDATE sessions SET agent_id=NULL WHERE id=? AND `+held, append([]any{id}, args...)...)
		return err
	}
	var active bool
	err := q.QueryRow(`SELECT EXISTS(SELECT 1 FROM agents WHERE id=? AND tombstone=0)`, a.agent).Scan(&active)
	switch {
	case err != nil:
	case known || in.Worker != nil:
		err = reject("Agent start reservation cannot replace a registered or worker identity")
	case !active:
		err = reject("selected Agent was tombstoned before registration")
	default:
		_, err = q.Exec(`UPDATE sessions SET agent_id=?, started_by=? WHERE id=?`, a.agent, protocol.StartedByAgentNet, id)
		return err
	}
	s.holds.add(a)
	return err
}

func (s *Store) SessionAgentPending(id string) (bool, error) {
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return false, err
	}
	var pending bool
	held, args := s.heldSQL("s")
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions s WHERE s.id=? AND `+held+`)`, append([]any{id}, args...)...).Scan(&pending)
	return pending, err
}

func (s *Store) bindStartedAgent(tx *sql.Tx, node string, p protocol.SessionAgentReservation) error {
	pending := s.holds.take(func(a *agentStart) bool {
		return a.node == node && a.harness == p.Harness && a.native == "" && a.ref == p.Ref && a.agent == p.AgentID
	})
	if pending == nil {
		return reject("unknown pending Agent start")
	}
	var id, actualCwd, worker string
	err := tx.QueryRow(`SELECT id,cwd,worker FROM sessions WHERE node_id=? AND harness=? AND native_id=?`, node, p.Harness, p.NativeID).Scan(&id, &actualCwd, &worker)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if id == "" {
		bound := *pending
		bound.native, bound.owner, bound.project = p.NativeID, p.Owner, p.Project
		s.holds.add(&bound)
	} else if slices.Contains(pending.existing, id) || worker != "" || actualCwd != pending.cwd {
		s.holds.add(pending)
		return reject("start must bind its newly created session")
	} else if _, err = tx.Exec(`UPDATE sessions SET agent_id=?, started_by=? WHERE id=?`, p.AgentID, protocol.StartedByAgentNet, id); err != nil {
		return err
	}
	// Release unrelated new sessions to their ordinary Repo-owner default. The
	// selected session already has its explicit Agent, or a native reservation.
	if _, err = tx.Exec(`UPDATE sessions SET agent_id=(SELECT lead_agent_id FROM repos WHERE id=sessions.repo_id) WHERE node_id=? AND harness=? AND worker='' AND agent_id IS NULL`, node, p.Harness); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ExpireSessionAgentStarts() ([]string, error) {
	now := s.now()
	expired := func(a *agentStart) bool { return !now.Before(a.expires) }
	var starts []*agentStart
	for a := s.holds.take(expired); a != nil; a = s.holds.take(expired) {
		starts = append(starts, a)
	}
	if len(starts) == 0 {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var nodes []string
	for _, a := range starts {
		if err = s.failAgentStart(tx, a, "Agent choice expired after two minutes"); err != nil {
			return nil, err
		}
		nodes = append(nodes, a.node)
	}
	return nodes, tx.Commit()
}

// failAgentStart records a start already out of the holds: the sessions it
// held get their Repo-owner default, and a Desk item keeps the reason.
func (s *Store) failAgentStart(tx *sql.Tx, a *agentStart, reason string) error {
	held, args := s.heldSQL("sessions")
	if _, err := tx.Exec(`UPDATE sessions SET agent_id=(SELECT lead_agent_id FROM repos WHERE id=sessions.repo_id) WHERE node_id=? AND harness=? AND worker='' AND agent_id IS NULL AND NOT `+held, append([]any{a.node, a.harness}, args...)...); err != nil {
		return err
	}
	var id string
	if a.native != "" {
		_ = tx.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness=? AND native_id=?`, a.node, a.harness, a.native).Scan(&id)
	}
	item, err := s.createInboxItem(tx, protocol.InboxItem{Kind: "check", SessionID: a.owner, Node: a.project, Text: s.agentChoiceFailureText(tx, a.ref, a.harness, a.project, id), Recommendation: reason})
	if err != nil {
		return err
	}
	a.item, a.reason = item.ID, reason
	s.holds.mu.Lock()
	defer s.holds.mu.Unlock()
	if s.holds.failed == nil {
		s.holds.failed = map[[2]string]*agentStart{}
	}
	s.holds.failed[[2]string{a.node, a.ref}] = a
	return nil
}

// When the native ID was lost, name the new harness session without guessing
// which concurrent session was selected. A later exact ID updates this same item.
func (s *Store) agentChoiceFailureText(q queryer, ref, harness, project, id string) string {
	label := "new " + harness + " session"
	agent := "Repo-owner Agent"
	if id != "" {
		var attached string
		if err := q.QueryRow(`SELECT s.name,COALESCE(s.repo_id,''),COALESCE(a.display_name,s.agent_id,'') FROM sessions s LEFT JOIN agents a ON a.id=s.agent_id WHERE s.id=?`, id).Scan(&label, &project, &attached); err == nil && attached != "" {
			return fmt.Sprintf("Agent choice for %s failed; %s uses the default %s", ref, label, attached)
		}
	}
	var name string
	if err := q.QueryRow(`SELECT COALESCE(a.display_name,r.lead_agent_id,'unassigned Agent') FROM repos r LEFT JOIN agents a ON a.id=r.lead_agent_id WHERE r.id=?`, project).Scan(&name); err == nil && name != "" {
		agent = name
	}
	return fmt.Sprintf("Agent choice for %s failed; %s uses the default %s", ref, label, agent)
}

func (s *Store) noteLateAgentNative(tx *sql.Tx, node string, p protocol.SessionAgentReservation) error {
	if p.NativeID == "" {
		return nil
	}
	s.holds.mu.Lock()
	if f := s.holds.failed[[2]string{node, p.Ref}]; f != nil && f.native == "" {
		f.native = p.NativeID
	}
	s.holds.mu.Unlock()
	var id string
	err := tx.QueryRow(`SELECT id FROM sessions WHERE node_id=? AND harness=? AND native_id=?`, node, p.Harness, p.NativeID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	held, args := s.heldSQL("sessions")
	if err == nil {
		_, err = tx.Exec(`UPDATE sessions SET agent_id=(SELECT lead_agent_id FROM repos WHERE id=sessions.repo_id) WHERE id=? AND worker='' AND agent_id IS NULL AND NOT `+held, append([]any{id}, args...)...)
	}
	if err != nil {
		return err
	}
	return s.updateFailedAgentSession(tx, node, p.Harness, p.NativeID, id)
}

// updateFailedAgentSession names a registered session in the Desk items of
// the failed starts that named its native ID.
func (s *Store) updateFailedAgentSession(q askDB, node, harness, native, id string) error {
	var failed []agentStart
	s.holds.mu.Lock()
	for _, f := range s.holds.failed {
		if f.node == node && f.harness == harness && f.native == native && native != "" {
			failed = append(failed, *f)
		}
	}
	s.holds.mu.Unlock()
	for _, f := range failed {
		if _, err := q.Exec(`UPDATE inbox_items SET text=? WHERE id=?`, s.agentChoiceFailureText(q, f.ref, harness, f.project, id), f.item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SessionAgentStartStatus(node, owner, ref string) (protocol.StartedSession, error) {
	if _, err := s.ExpireSessionAgentStarts(); err != nil {
		return protocol.StartedSession{}, err
	}
	if err := s.requireMachineOwner(node, owner, "owner authority required"); err != nil {
		return protocol.StartedSession{}, err
	}
	r := protocol.StartedSession{ID: ref}
	s.holds.mu.Lock()
	defer s.holds.mu.Unlock()
	if f := s.holds.failed[[2]string{node, ref}]; f != nil {
		r.Status, r.Error = "failed", f.reason
	}
	return r, nil
}
