package gateway

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// startHolds are the short holds of sessions being started: a repo slot
// (ReserveRepoStart) and the owner's Agent choice (ReserveSessionAgent), with
// the choices that failed. No session row exists before the harness registers,
// so they live in memory, and a gateway restart drops them. mu guards only the
// maps: code that holds it never waits on the database, because a registration
// takes it while its transaction holds the one write connection.
type startHolds struct {
	mu     sync.Mutex
	repo   map[[2]string]*repoHold   // machine, start reference
	agents map[[3]string]*agentStart // machine, harness, native ID ('' until known)
	failed map[[2]string]*agentStart // machine, start reference
}

type repoHold struct {
	repo, harness, native string
	expires               time.Time
}

// ReserveRepoStart serializes admission with the live count and other launches.
// Registration itself remains unrestricted, including terminal sessions.
func (s *Store) ReserveRepoStart(node string, p protocol.RepoStartReservation) (protocol.RepoStartResult, error) {
	var out protocol.RepoStartResult
	if err := s.requireMachineOwner(node, p.Owner, "session start requires the owner of this machine"); err != nil {
		return out, err
	}
	if p.Ref == "" {
		return out, reject("session start needs a reference")
	}
	var max, live int
	var online bool
	var name string
	err := s.db.QueryRow(`SELECT COALESCE((SELECT CAST(value AS INTEGER) FROM setting_values WHERE key='max_agents' AND level='repo' AND scope=? AND item=''),0),
		(SELECT COUNT(*) FROM sessions s WHERE s.repo_id=? AND s.online=1 AND s.worker='' AND s.harness NOT IN ('owner','cto','bot') AND s.id<>COALESCE((SELECT value FROM gateway_state WHERE key='cto_session'),'') AND NOT EXISTS(SELECT 1 FROM project_mergers m WHERE m.session_id=s.id)),
		EXISTS(SELECT 1 FROM sessions WHERE node_id=? AND harness=? AND native_id=? AND native_id<>'' AND online=1),
		COALESCE((SELECT display_name FROM repos WHERE id=?),?)`, p.Project, p.Project, node, p.Harness, p.NativeID, p.Project, p.Project).Scan(&max, &live, &online, &name)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	h, key, now := &s.holds, [2]string{node, p.Ref}, s.now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if p.Release || p.NativeID != "" {
		if hold := h.repo[key]; hold != nil && !p.Release && !online {
			hold.native = p.NativeID
		} else {
			delete(h.repo, key)
		}
		return out, nil
	}
	if max == 0 {
		return out, nil
	}
	count := live
	for k, hold := range h.repo {
		if !now.Before(hold.expires) {
			delete(h.repo, k)
		} else if hold.repo == p.Project {
			count++
		}
	}
	if out.Reserved = h.repo[key] != nil; out.Reserved {
		return out, nil
	}
	if count >= max {
		value, _ := json.Marshal(max + 1)
		expect, _ := json.Marshal(max)
		out.Status = "failed"
		out.Error = fmt.Sprintf("%s already has %d agents (max %d). Stop one, or raise the max to %d.", name, count, max, max+1)
		out.SuggestedChange = &protocol.SettingsChange{Key: "max_agents", Level: "repo", Scope: p.Project, Value: value, Expect: expect}
		return out, nil
	}
	if !p.Check {
		if h.repo == nil {
			h.repo = map[[2]string]*repoHold{}
		}
		h.repo[key] = &repoHold{repo: p.Project, harness: p.Harness, expires: now.Add(2 * time.Minute)}
		out.Reserved = true
	}
	return out, nil
}

// releaseRepoHold ends the hold a registering session used and says whether
// it had one with its native ID. Without one, a repo given here (a new
// session: Codex can hand a terminal over before it has a native ID) ends the
// oldest unnamed hold of its harness and repo.
func (h *startHolds) releaseRepoHold(node, harness, native, repo string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	var oldest [2]string
	for k, hold := range h.repo {
		if k[0] != node || hold.harness != harness {
			continue
		}
		if native != "" && hold.native == native {
			delete(h.repo, k)
			return true
		}
		if repo != "" && hold.native == "" && hold.repo == repo && (oldest[0] == "" || hold.expires.Before(h.repo[oldest].expires)) {
			oldest = k
		}
	}
	delete(h.repo, oldest)
	return false
}

// requireMachineOwner refuses a start that does not come from the owner
// session of the machine.
func (s *Store) requireMachineOwner(node, owner, refusal string) error {
	var allowed bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND node_id=? AND harness='owner' AND worker='')`, owner, node).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return reject("%s", refusal)
	}
	return nil
}
