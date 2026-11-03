package gateway

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/settings"
)

func settingValue(s string) json.RawMessage { return json.RawMessage(s) }
func TestSettingsWritesUndoAndHistory(t *testing.T) {
	s, _ := newTestStore(t)
	child := SettingsChange{Key: "delivery.merge_authority", Level: "repo", Scope: "alpha", Value: settingValue(`"full"`)}
	first, err := s.WriteSettings([]SettingsChange{child}, "owner", "first")
	if err != nil || first.Revision != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	parent := SettingsChange{Key: child.Key, Level: "network", Scope: "network", Value: settingValue(`"delegated"`)}
	result, err := s.WriteSettings([]SettingsChange{parent}, "owner", "tighten")
	if err != nil || len(result.Moved) != 1 || result.Moved[0].Scope != "alpha" {
		t.Fatalf("move: %+v %v", result, err)
	}
	snap, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	e, _ := snap.Resolve(child.Key, settings.Where{Repo: "alpha"})
	if string(e.Value) != `"delegated"` {
		t.Fatal(e)
	}
	child.Expect = settingValue(`"delegated"`)
	if _, err = s.WriteSettings([]SettingsChange{child}, "owner", "loosen child"); err == nil {
		t.Fatal("loose child accepted")
	}
	parent.Expect = settingValue(`"ask"`)
	if _, err = s.WriteSettings([]SettingsChange{parent}, "owner", "stale"); err == nil {
		t.Fatal("stale accepted")
	}
	parent.Expect = settingValue(`"delegated"`)
	parent.Value = settingValue(`"full"`)
	relax, err := s.WriteSettings([]SettingsChange{parent}, "owner", "relax")
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = s.SettingsSnapshot()
	e, _ = snap.Resolve(child.Key, settings.Where{Repo: "alpha"})
	if string(e.Value) != `"delegated"` {
		t.Fatal("relax rewrote child", e)
	}
	parent.Expect = parent.Value
	noop, err := s.WriteSettings([]SettingsChange{parent}, "owner", "noop")
	if err != nil || noop.Revision != relax.Revision {
		t.Fatalf("noop %+v %v", noop, err)
	}
	if _, err = s.WriteSettings([]SettingsChange{child}, "cto", "rule"); err == nil {
		t.Fatal("CTO changed rule")
	}
	advice := SettingsChange{Key: "guidance.peer_help_direct", Level: "network", Scope: "network", Value: settingValue(`"Reply {{reply}}."`)}
	if _, err = s.WriteSettings([]SettingsChange{advice}, "cto", "advice"); err != nil {
		t.Fatal(err)
	}
	undo, err := s.UndoSettings(relax.Revision, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UndoSettings(undo.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"UPDATE setting_history SET reason='x'", "DELETE FROM setting_history"} {
		if _, err = s.db.Exec(q); err == nil {
			t.Fatal("history modified")
		}
	}
}
func TestSettingsMaturityRevision(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.SettingsSnapshot()
	if err := s.SetCharter("alpha", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	after, err := s.SettingsSnapshot()
	if err != nil || after.Revision != before.Revision+1 || after.Maturity["alpha"] != "critical" {
		t.Fatalf("%+v %v", after, err)
	}
	pushes := 0
	s.settingsHook = func() { pushes++ }
	if err := s.SetCharter("alpha", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	if pushes != 0 {
		t.Fatal("unchanged maturity triggered a push")
	}
}

func TestSettingsSnapshotSyncLink(t *testing.T) {
	g, url, _, token := newLinkGateway(t, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	sendLinkSession(t, ws)
	readSync := func() settings.Snapshot {
		t.Helper()
		for {
			_, raw, err := ws.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var h struct {
				Type string `json:"type"`
			}
			if err = json.Unmarshal(raw, &h); err != nil {
				t.Fatal(err)
			}
			if h.Type == "settings.sync" {
				var snap settings.Snapshot
				if err = json.Unmarshal(raw, &snap); err != nil {
					t.Fatal(err)
				}
				return snap
			}
		}
	}
	first := readSync()
	if first.Revision != 0 {
		t.Fatal(first.Revision)
	}
	_, err = g.store.WriteSettings([]SettingsChange{{Key: "workers.auto_continue", Level: "network", Scope: "network", Value: settingValue("false")}}, "owner", "test")
	if err != nil {
		t.Fatal(err)
	}
	next := readSync()
	if next.Revision != 1 || len(next.Values) != 1 || string(next.Values[0].Value) != "false" {
		t.Fatal(next)
	}
	if err = g.store.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if err = g.store.SetCharter("alpha", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	next = readSync()
	if next.Revision != 2 || next.Maturity["alpha"] != "critical" {
		t.Fatal(next)
	}
}
func TestSettingsMaturityUndoRemove(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCharter("alpha", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM graph_history WHERE type='charter' AND attr='maturity'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.GraphUndo(id, "owner"); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.SettingsSnapshot()
	if snap.Revision != 2 || snap.Maturity["alpha"] != "" {
		t.Fatal(snap)
	}
	if err := s.SetCharter("alpha", "maturity", "mature", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.SettingsSnapshot()
	if snap.Revision != 4 || snap.Maturity["alpha"] != "" {
		t.Fatal(snap)
	}
}

func TestSettingsUndoTightensAndMovesChildren(t *testing.T) {
	s, _ := newTestStore(t)
	network := SettingsChange{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: settingValue(`"delegated"`)}
	if _, err := s.WriteSettings([]SettingsChange{network}, "owner", "tighten"); err != nil {
		t.Fatal(err)
	}
	network.Expect, network.Value = settingValue(`"delegated"`), settingValue(`"full"`)
	relaxed, err := s.WriteSettings([]SettingsChange{network}, "owner", "relax")
	if err != nil {
		t.Fatal(err)
	}
	child := SettingsChange{Key: network.Key, Level: "repo", Scope: "alpha", Value: settingValue(`"full"`)}
	if _, err = s.WriteSettings([]SettingsChange{child}, "owner", "child"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UndoSettings(relaxed.Revision, "cto"); err == nil {
		t.Fatal("CTO undid rule")
	}
	undo, err := s.UndoSettings(relaxed.Revision, "owner")
	if err != nil || len(undo.Moved) != 1 || !undo.Moved[0].Rewritten {
		t.Fatalf("%+v %v", undo, err)
	}
	if _, err = s.UndoSettings(undo.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.SettingsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	e, err := snap.Resolve(network.Key, settings.Where{Repo: "alpha"})
	if err != nil || string(e.Value) != `"full"` {
		t.Fatalf("%+v %v", e, err)
	}
}

func TestSettingsReportsMaturityChanges(t *testing.T) {
	s, _ := newTestStore(t)
	for _, node := range []string{"early", "critical"} {
		if err := s.AddArchitect(node, "owner"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetCharter(node, "maturity", node, "owner"); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: settingValue(`"delegated"`)}}, "owner", "tighten")
	if err != nil || len(result.Moved) != 1 || result.Moved[0].Scope != "early" || result.Moved[0].Rewritten {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSettingsReportsReposWithoutMaturity(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO graph_nodes(name,kind,created_at) VALUES('empty-repo','component',0)`); err != nil {
		t.Fatal(err)
	}
	result, err := s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: settingValue(`"delegated"`)}}, "owner", "tighten")
	if err != nil || len(result.Moved) != 1 || result.Moved[0].Scope != "empty-repo" {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSettingsTightenIgnoresBrokenChild(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('delivery.merge_authority','repo','alpha','','42',0)`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.WriteSettings([]SettingsChange{{Key: "delivery.merge_authority", Level: "network", Scope: "network", Value: settingValue(`"delegated"`)}}, "owner", "tighten")
	if err != nil || len(result.Moved) != 1 || result.Moved[0].Scope != "alpha" {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestSettingsSnapshotConsistentTransaction(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 1; i <= 40; i++ {
			tx, err := s.db.Begin()
			if err != nil {
				done <- err
				return
			}
			maturity, value := "growing", "true"
			if i%2 != 0 {
				maturity, value = "critical", "false"
			}
			_, err = tx.Exec(`INSERT INTO graph_charter(node,attr,value,updated_at,updated_by) VALUES('alpha','maturity',?,0,'owner') ON CONFLICT(node,attr) DO UPDATE SET value=excluded.value`, maturity)
			if err == nil {
				_, err = tx.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('workers.auto_continue','network','network','',?,?) ON CONFLICT(key,level,scope,item) DO UPDATE SET value=excluded.value,revision=excluded.revision`, value, i)
			}
			if err == nil {
				_, err = tx.Exec(`UPDATE setting_revision SET revision=? WHERE id=1`, i)
			}
			if err != nil {
				tx.Rollback()
				done <- err
				return
			}
			if err = tx.Commit(); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 50; i++ {
		snap, err := s.SettingsSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		if snap.Revision == 0 {
			continue
		}
		wantMaturity, wantValue := "growing", "true"
		if snap.Revision%2 != 0 {
			wantMaturity, wantValue = "critical", "false"
		}
		if snap.Maturity["alpha"] != wantMaturity || len(snap.Values) != 1 || string(snap.Values[0].Value) != wantValue {
			t.Fatalf("mixed snapshot: %+v", snap)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
