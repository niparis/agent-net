package gateway

import (
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
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
	child.Value = settingValue(`"delegated"`)
	if _, err = s.WriteSettings([]SettingsChange{child}, "cto", "rule"); err != nil {
		t.Fatalf("CTO rule: %v", err)
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
	if err := s.seedLegacyCharter("alpha", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	after, err := s.SettingsSnapshot()
	if err != nil || after.Revision != before.Revision+1 || after.Maturity["alpha"] != "critical" {
		t.Fatalf("%+v %v", after, err)
	}
	pushes := 0
	s.settingsHook = func() { pushes++ }
	if err := s.seedLegacyCharter("alpha", "maturity", "critical", "owner"); err != nil {
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
	_, err = g.handle(ctx, &nodeConn{}, "settings.write", []byte(`{"changes":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false}],"reason":"test","by":"owner"}`))
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
	if err = g.store.seedLegacyCharter("alpha", "maturity", "critical", "owner"); err != nil {
		t.Fatal(err)
	}
	next = readSync()
	if next.Revision != 2 || next.Maturity["alpha"] != "critical" {
		t.Fatal(next)
	}
}

// The old daemon's read loop handles result and known push types, and has no
// default case. This peer deliberately has no settings.sync handler and sends
// no acknowledgment for that frame.
func TestSettingsSyncLegacyPeer(t *testing.T) {
	_, url, _, token := newLinkGateway(t, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	sendLinkSession(t, ws)
	seenSync := false
	for {
		_, raw, err := ws.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var h struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			OK   bool   `json:"ok"`
		}
		if err = json.Unmarshal(raw, &h); err != nil {
			t.Fatal(err)
		}
		// This observation belongs to the test harness, not a peer handler.
		if h.Type == "settings.sync" && !seenSync {
			seenSync = true
			if err = ws.Write(ctx, websocket.MessageText, []byte(`{"type":"session.sync","id":"legacy-again","sessions":[]}`)); err != nil {
				t.Fatal(err)
			}
		}
		switch h.Type {
		case "result":
			if h.ID == "legacy-again" {
				if !h.OK {
					t.Fatal("legacy peer's second sync was rejected")
				}
				if !seenSync {
					t.Fatal("no settings frame received")
				}
				return
			}
		case "mcp.sync", "node.name":
			// Known pushes also do not require an answer.
		}
	}
}
func TestSettingsMaturityUndoRemove(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.AddArchitect("alpha", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.seedLegacyCharter("alpha", "maturity", "critical", "owner"); err != nil {
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
	undo, err := s.UndoSettings(relaxed.Revision, "cto")
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
		if err := s.seedLegacyCharter(node, "maturity", node, "owner"); err != nil {
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
	_, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('delivery.pipeline.merge.approver','repo','alpha','','42',0)`)
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

func TestSettingsWriteUndoFrames(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	nc := &nodeConn{}
	write := `{"changes":[{"key":"guidance.peer_help_direct","level":"network","scope":"network","value":"Please reply {{reply}}."}],"reason":"test","by":"cto"}`
	result, err := g.handle(context.Background(), nc, "settings.write", []byte(write))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, _ := json.Marshal(result)
	if !json.Valid(b) || !jsonContains(b, `"revision":1`) {
		t.Fatalf("write result: %s", b)
	}
	_, err = g.handle(context.Background(), nc, "settings.write", []byte(write))
	if err == nil || !strings.Contains(err.Error(), "current") {
		t.Fatalf("stale write: %v", err)
	}
	if _, err = g.handle(context.Background(), nc, "settings.undo", []byte(`{"revision":1,"by":"cto"}`)); err != nil {
		t.Fatalf("undo: %v", err)
	}
}

func jsonContains(b []byte, want string) bool { return strings.Contains(string(b), want) }

func TestSettingsFramesAtomicActorAndMoves(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	ctx := context.Background()
	child := `{"changes":[{"key":"delivery.merge_authority","level":"repo","scope":"alpha","value":"full"}],"by":"owner"}`
	if _, err := g.handle(ctx, &nodeConn{}, "settings.write", []byte(child)); err != nil {
		t.Fatal(err)
	}
	cto := `{"changes":[{"key":"delivery.merge_authority","level":"network","scope":"network","value":"delegated"}],"by":"cto"}`
	got, err := g.handle(ctx, &nodeConn{}, "settings.write", []byte(cto))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"scope":"alpha"`) || !strings.Contains(string(raw), `"rewritten":true`) {
		t.Fatalf("moves: %s", raw)
	}
	bad := `{"changes":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false},{"key":"delivery.merge_authority","level":"network","scope":"network","value":"ask","expect":"full"}],"by":"owner"}`
	if _, err = g.handle(ctx, &nodeConn{}, "settings.write", []byte(bad)); err == nil || !strings.Contains(err.Error(), `current "delegated"`) {
		t.Fatalf("conflict: %v", err)
	}
	snap, _ := s.SettingsSnapshot()
	if snap.Revision != 2 {
		t.Fatalf("partial batch: %+v", snap)
	}
	e, _ := snap.Resolve("workers.auto_continue", settings.Where{})
	if string(e.Value) != "true" {
		t.Fatal("partial batch applied")
	}
	if _, err = g.handle(ctx, &nodeConn{}, "settings.undo", []byte(`{"revision":2,"by":"cto"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsBotReadPublicValues(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	_, err := s.WriteSettings([]SettingsChange{{Key: "guidance.peer_help_direct", Level: "network", Scope: "network", Value: settingValue(`"Answer {{reply}}."`)}}, "cto", "test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.botToolCall(context.Background(), "s_bot", "agent_settings_read", map[string]any{"key": "guidance.peer_help_direct"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	for _, want := range []string{`"source":"network"`, `"actor":"cto"`, `"reason":"test"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %s: %s", want, raw)
		}
	}
}

func TestSettingsConflictNamesUnsetValue(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.WriteSettings([]SettingsChange{{Key: "workers.auto_continue", Level: "network", Scope: "network", Value: settingValue("false"), Expect: settingValue("true")}}, "owner", "test")
	if err == nil || !strings.Contains(err.Error(), "current not set") {
		t.Fatalf("conflict: %v", err)
	}
}

func TestSettingsFramesRejectUnknownActors(t *testing.T) {
	for _, tc := range []struct{ key, value string }{{"workers.auto_continue", "false"}, {"guidance.intro_text", `"hello"`}} {
		for _, actor := range []string{"", "agent", "CTO", "agent-net cutover", "owner (CTO proposal p_bad)", "owner (CTO proposal p_)", "owner (CTO proposal p_1) extra"} {
			t.Run(tc.key+"/"+actor, func(t *testing.T) {
				s, _ := newTestStore(t)
				g := &Gateway{store: s}
				ctx := context.Background()
				change := SettingsChange{Key: tc.key, Level: "network", Scope: "network", Value: settingValue(tc.value)}
				frame, _ := json.Marshal(map[string]any{"changes": []protocol.SettingsChange{change}, "by": actor})
				if _, err := g.handle(ctx, &nodeConn{}, "settings.write", frame); err == nil {
					t.Fatalf("actor %q wrote %s", actor, tc.key)
				}
				if _, err := g.handle(ctx, &nodeConn{}, "settings.validate", frame); err == nil {
					t.Fatalf("actor %q validated a write to %s", actor, tc.key)
				}
				seeded, err := s.WriteSettings([]SettingsChange{change}, "owner", "fixture")
				if err != nil {
					t.Fatal(err)
				}
				undo, _ := json.Marshal(map[string]any{"revision": seeded.Revision, "by": actor})
				if _, err := g.handle(ctx, &nodeConn{}, "settings.undo", undo); err == nil {
					t.Fatalf("actor %q undid %s", actor, tc.key)
				}
				snap, _ := s.SettingsSnapshot()
				if snap.Revision != seeded.Revision {
					t.Fatalf("state changed: %+v", snap)
				}
			})
		}
	}
}

func TestSettingsExpectNullMeansUnset(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	ctx := context.Background()
	frame := `{"by":"owner","changes":[{"key":"workers.auto_continue","level":"network","scope":"network","value":false,"expect":null}]}`
	if _, err := g.handle(ctx, &nodeConn{}, "settings.write", []byte(frame)); err != nil {
		t.Fatalf("null expected absent: %v", err)
	}
	if _, err := g.handle(ctx, &nodeConn{}, "settings.write", []byte(frame)); err == nil || !strings.Contains(err.Error(), "current false") {
		t.Fatalf("null must conflict with a stored value: %v", err)
	}
}

func TestSettingsCTORejectsGuidanceMarkers(t *testing.T) {
	s, _ := newTestStore(t)
	g := &Gateway{store: s}
	for _, marker := range []string{"<!-- agent-net:begin -->", "<!-- agent-net:end -->", "[agent-net]", "[agent-net worker notice]", "[ AGENT-NET]"} {
		val, _ := json.Marshal("Help. " + marker)
		frame, _ := json.Marshal(protocol.SettingsWrite{By: "cto", Changes: []protocol.SettingsChange{{Key: "guidance.intro_text", Level: "network", Scope: "network", Value: val}}})
		if _, err := g.handle(context.Background(), &nodeConn{}, "settings.write", frame); err == nil {
			t.Errorf("accepted %q", marker)
		}
	}
}

func TestSettingsValidationIsTransactionalAndSilent(t *testing.T) {
	s, _ := newTestStore(t)
	pushes := 0
	s.settingsHook = func() { pushes++ }
	change := SettingsChange{Key: "workers.auto_continue", Level: "network", Scope: "network", Value: settingValue("false")}
	if err := s.ValidateSettings([]SettingsChange{change}, "owner"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.SettingsSnapshot()
	if err != nil || snap.Revision != 0 || len(snap.Values) != 0 || pushes != 0 {
		t.Fatalf("validation changed state: %+v pushes=%d err=%v", snap, pushes, err)
	}
	hist, err := s.SettingsHistory("", 0, true)
	if err != nil || len(hist) != 0 {
		t.Fatalf("validation wrote history: %+v %v", hist, err)
	}
}

func TestSettingsFramesAllowOnlyNamedActors(t *testing.T) {
	for _, actor := range []string{"owner", "owner (CTO proposal p_17)", "cto", "CTO (minor)", "CTO (owner window)"} {
		t.Run(actor, func(t *testing.T) {
			s, _ := newTestStore(t)
			g := &Gateway{store: s}
			q := protocol.SettingsWrite{By: actor, Changes: []SettingsChange{{Key: "guidance.intro_text", Level: "network", Scope: "network", Value: settingValue(`"hello"`)}}}
			raw, _ := json.Marshal(q)
			result, err := g.handle(context.Background(), &nodeConn{}, "settings.write", raw)
			if err != nil {
				t.Fatal(err)
			}
			rev := result.(SettingsResult).Revision
			undo, _ := json.Marshal(protocol.SettingsUndo{Revision: rev, By: actor})
			if _, err = g.handle(context.Background(), &nodeConn{}, "settings.undo", undo); err != nil {
				t.Fatal(err)
			}
			rule := protocol.SettingsWrite{By: actor, Changes: []SettingsChange{{Key: "workers.auto_continue", Level: "network", Scope: "network", Value: settingValue("false")}}}
			raw, _ = json.Marshal(rule)
			_, err = g.handle(context.Background(), &nodeConn{}, "settings.write", raw)
			if (strings.HasPrefix(actor, "owner") || actor == "cto") && err != nil {
				t.Fatalf("owner rule: %v", err)
			}
			if !strings.HasPrefix(actor, "owner") && actor != "cto" && err == nil {
				t.Fatal("CTO changed rule")
			}
		})
	}
}

func TestSettingsUndoRemovesLegacyGuidanceMarker(t *testing.T) {
	s, _ := newTestStore(t)
	badJSON, _ := json.Marshal("Bad <!-- agent-net:end --> text")
	bad := string(badJSON)
	if _, err := s.db.Exec(`INSERT INTO setting_values(key,level,scope,item,value,revision) VALUES('guidance.intro_text','network','network','',?,1)`, bad); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO setting_history(revision,seq,key,level,scope,item,old_value,new_value,actor,reason,cause,at) VALUES(1,1,'guidance.intro_text','network','network','',NULL,?,'cto','old write','write',1)`, bad); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE setting_revision SET revision=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	result, err := s.UndoSettings(1, "cto")
	if err != nil {
		t.Fatalf("undo legacy marker: %v", err)
	}
	if result.Revision != 2 {
		t.Fatal(result)
	}
	snap, err := s.SettingsSnapshot()
	if err != nil || len(snap.Values) != 0 {
		t.Fatalf("legacy value remains: %+v %v", snap, err)
	}
	if _, err = s.UndoSettings(2, "cto"); err == nil {
		t.Fatal("undo restored forbidden marker")
	}
}

func TestWorkerBriefRulesRequireOwner(t *testing.T) {
	keys := []string{"workers.brief_codex_complete", "workers.brief_approval", "workers.brief_handback", "workers.brief_handback_partial", "workers.brief_blocked", "workers.brief_codex_final", "workers.brief_codex_blocked", "workers.brief_no_delegation"}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			s, _ := newTestStore(t)
			e, err := (settings.Snapshot{}).Resolve(key, settings.Where{})
			if err != nil {
				t.Fatal(err)
			}
			change := SettingsChange{Key: key, Level: "network", Scope: "network", Value: e.Value}
			for _, actor := range []string{"CTO (minor)", "CTO (owner window)"} {
				if _, err = s.WriteSettings([]SettingsChange{change}, actor, "test"); err == nil {
					t.Errorf("%s changed protected %s without approval", actor, key)
				}
			}
			if _, err = s.WriteSettings([]SettingsChange{change}, "cto", "test"); err != nil {
				t.Fatalf("CTO network write: %v", err)
			}
			change.Level, change.Scope = "repo", "shop"
			var original string
			if err = json.Unmarshal(e.Value, &original); err != nil {
				t.Fatal(err)
			}
			change.Value, _ = json.Marshal(original + " Project wording.")
			if _, err = s.WriteSettings([]SettingsChange{change}, "owner", "test"); err != nil {
				t.Fatalf("owner repo write: %v", err)
			}
			snap, err := s.SettingsSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			effective, err := snap.Resolve(key, settings.Where{Repo: "shop"})
			if err != nil || string(effective.Value) != string(change.Value) || effective.Source != "repo shop" {
				t.Fatalf("repo precedence: %+v %v", effective, err)
			}
		})
	}
}
