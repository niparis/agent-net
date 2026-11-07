package gateway

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/settings"
)

func TestMaxSessionStartReservationAndSuggestion(t *testing.T) {
	s := teamStore(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE repo_id='demo'`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`1`)}}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	p := protocol.RepoStartReservation{Owner: owner, Ref: "start_one", Project: "demo", Harness: "pi"}
	res, err := s.ReserveRepoStart(node, p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || res.Error != "demo already has 1 agents (max 1). Stop one, or raise the max to 2." || res.SuggestedChange == nil || string(res.SuggestedChange.Value) != "2" || res.SuggestedChange.Scope != "demo" {
		t.Fatalf("refusal: %+v", res)
	}
	if _, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "terminal", Repo: protocol.RepoInfo{Project: "demo"}}); err != nil {
		t.Fatalf("terminal registration refused: %v", err)
	}
	info, err := s.RepoAgentLimit("demo")
	if err != nil || info.AgentCount != 2 || !info.OverMax {
		t.Fatalf("terminal count: %+v %v", info, err)
	}
}

func TestMaxConcurrentSessionStarts(t *testing.T) {
	s := teamStore(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE repo_id='demo'`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`2`)}}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan protocol.RepoStartResult, 2)
	errors := make(chan error, 2)
	for _, ref := range []string{"start_a", "start_b"} {
		wg.Add(1)
		go func(ref string) {
			defer wg.Done()
			res, err := s.ReserveRepoStart(node, protocol.RepoStartReservation{Owner: owner, Ref: ref, Project: "demo", Harness: "pi"})
			results <- res
			errors <- err
		}(ref)
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	accepted, refused := 0, 0
	for res := range results {
		if res.Reserved {
			accepted++
		}
		if res.Status == "failed" {
			refused++
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("accepted %d refused %d", accepted, refused)
	}
}

func TestMaxAgentsSettingIsRepoOnly(t *testing.T) {
	def, ok := settings.DefinitionFor("max_agents")
	if !ok || len(def.Levels) != 1 || def.Levels[0] != "repo" {
		t.Fatalf("setting levels: %+v", def.Levels)
	}
	s := teamStore(t)
	if _, err := s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "network", Scope: "network", Value: json.RawMessage(`3`)}}, "owner", "invalid"); err == nil {
		t.Fatal("network maximum accepted")
	}
}

func TestMaxReservationTracksRegisteredSession(t *testing.T) {
	s := teamStore(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE repo_id='demo'`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`2`)}}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	q := protocol.RepoStartReservation{Owner: owner, Ref: "first", Project: "demo", Harness: "pi"}
	first, err := s.ReserveRepoStart(node, q)
	if err != nil || !first.Reserved {
		t.Fatalf("reserve: %+v %v", first, err)
	}
	newID, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "pi", NativeID: "new", Repo: protocol.RepoInfo{Project: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	q.NativeID = "new"
	if _, err = s.ReserveRepoStart(node, q); err != nil {
		t.Fatal(err)
	}
	next := protocol.RepoStartReservation{Owner: owner, Ref: "second", Project: "demo", Harness: "pi"}
	out, err := s.ReserveRepoStart(node, next)
	if err != nil || out.Error != "demo already has 2 agents (max 2). Stop one, or raise the max to 3." {
		t.Fatalf("registered counted twice: %+v %v", out, err)
	}
	if _, err = s.db.Exec(`UPDATE sessions SET online=0 WHERE id=?`, newID); err != nil {
		t.Fatal(err)
	}
	out, err = s.ReserveRepoStart(node, next)
	if err != nil || !out.Reserved {
		t.Fatalf("free place: %+v %v", out, err)
	}
	next.Release = true
	if _, err = s.ReserveRepoStart(node, next); err != nil {
		t.Fatal(err)
	}
	next.Ref = "third"
	next.Release = false
	out, err = s.ReserveRepoStart(node, next)
	if err != nil || !out.Reserved {
		t.Fatalf("released place: %+v %v", out, err)
	}
}

func TestMaxCountExcludesWorkerMergerAndCTO(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.SetMerger(protocol.MergerSetRequest{Project: "demo", NodeID: r.a, SessionID: r.sonnet}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.db.Exec(`INSERT INTO owner_settings(key,value,updated_at) VALUES('cto_session',?,0) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, r.caller); err != nil {
		t.Fatal(err)
	}
	limit, err := r.s.RepoAgentLimit("demo")
	if err != nil || limit.AgentCount != 1 {
		t.Fatalf("excluded sessions: %+v %v", limit, err)
	}
}

func TestLeadSessionReadExposesCapacity(t *testing.T) {
	s := teamStore(t)
	_, err := s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`3`)}}, "owner", "limit")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.FindSessions(protocol.SessionsFindRequest{Project: "demo"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("sessions: %+v %v", page, err)
	}
	row := page.Items[0]
	if !row.RepoLead || row.RepoMaxAgents != 3 || row.RepoAgentCount != 1 || row.AgentID != "demo" {
		t.Fatalf("row: %+v", row)
	}
}

func TestMaxUnboundReservationConsumedByNewSession(t *testing.T) {
	s := teamStore(t)
	var node string
	if err := s.db.QueryRow(`SELECT node_id FROM sessions WHERE repo_id='demo'`).Scan(&node); err != nil {
		t.Fatal(err)
	}
	owner, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "owner", NativeID: protocol.OwnerNativeID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.WriteSettings([]SettingsChange{{Key: "max_agents", Level: "repo", Scope: "demo", Value: json.RawMessage(`2`)}}, "owner", "limit"); err != nil {
		t.Fatal(err)
	}
	q := protocol.RepoStartReservation{Owner: owner, Ref: "first", Project: "demo", Harness: "codex"}
	out, err := s.ReserveRepoStart(node, q)
	if err != nil || !out.Reserved {
		t.Fatalf("reserve: %+v %v", out, err)
	}
	if _, err = s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "new", Repo: protocol.RepoInfo{Project: "demo"}}); err != nil {
		t.Fatal(err)
	}
	q.Ref = "second"
	out, err = s.ReserveRepoStart(node, q)
	if err != nil || out.Error != "demo already has 2 agents (max 2). Stop one, or raise the max to 3." {
		t.Fatalf("unbound counted twice: %+v %v", out, err)
	}
}
