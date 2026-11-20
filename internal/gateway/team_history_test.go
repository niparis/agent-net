package gateway

import (
	"reflect"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// teamLines shows each Team history row on one line, so a test compares a
// whole view in order. An empty field shows as "-", a JSON snapshot as "{json}".
func teamLines(history []protocol.TeamChange) []string {
	out := []string{}
	for _, h := range history {
		member := "-"
		if h.Member != nil {
			member = h.Member.Kind + ":" + h.Member.ID
		}
		out = append(out, strings.Join([]string{h.Action, teamShown(h.TeamID), teamShown(h.OldTeamID), member, teamShown(h.OldValue), teamShown(h.NewValue), h.By}, "|"))
	}
	return out
}

func teamShown(v string) string {
	switch {
	case v == "":
		return "-"
	case strings.HasPrefix(v, "{"):
		return "{json}"
	}
	return v
}

// TestTeamChangesAreOneGraphRowEach: an owner's Team change is one graph_history
// row, the same as a CTO's. team_history is no longer written.
func TestTeamChangesAreOneGraphRowEach(t *testing.T) {
	s := teamStore(t)
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "alpha", Name: "Alpha"})
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "alpha", Member: &repo})
	lead := protocol.GraphRef{Kind: "agent", ID: "demo"}
	if err := s.SetLead(protocol.LeadSetRequest{Ref: protocol.GraphRef{Kind: "team", ID: "alpha"}, Lead: &lead}, "owner", false); err != nil {
		t.Fatal(err)
	}
	teamWrite(t, s, protocol.TeamWrite{Action: "rename", ID: "alpha", Name: "Renamed", ExpectName: "Alpha"})

	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM graph_history WHERE type IN ('team','team_undo','lead') AND actor='owner'`).Scan(&rows); err != nil || rows != 4 {
		t.Fatalf("owner Team changes in graph_history: %d rows, %v; want one row per change", rows, err)
	}
	h, err := s.TeamHistory("alpha", 100)
	want := []string{
		"rename|alpha|-|-|{json}|{json}|owner",
		"lead|alpha|-|-|-|demo|owner",
		"membership|alpha|-|repo:demo|-|alpha|owner",
		"create|alpha|-|-|-|{json}|owner",
	}
	if err != nil || !reflect.DeepEqual(teamLines(h), want) {
		t.Fatalf("history: %q, %v; want %q", teamLines(h), err, want)
	}
}

// TestTeamHistoryViewAfterMoveAndUndo pins the view of a move and its undo. A
// move shows in both Teams, and the undo of it is an "undo" row in both.
func TestTeamHistoryViewAfterMoveAndUndo(t *testing.T) {
	s := teamStore(t)
	repo := protocol.GraphRef{Kind: "repo", ID: "demo"}
	for _, q := range []protocol.TeamWrite{
		{Action: "create", ID: "alpha", Name: "Alpha"},
		{Action: "membership", ID: "alpha", Member: &repo},
		{Action: "create", ID: "beta", Name: "Beta"},
		{Action: "membership", ID: "beta", Member: &repo, ExpectTeam: "alpha"},
	} {
		if err := s.WriteTeam(q, "cto"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.GraphUndo(0, "owner"); err != nil {
		t.Fatal(err)
	}
	if team, err := s.MemberTeam(repo); err != nil || team != "alpha" {
		t.Fatalf("member after undo: %q, %v", team, err)
	}
	move := []string{
		"undo|alpha|beta|repo:demo|beta|alpha|owner",
		"membership|beta|alpha|repo:demo|alpha|beta|cto",
	}
	alpha, err := s.TeamHistory("alpha", 100)
	wantAlpha := append(append([]string{}, move...),
		"membership|alpha|-|repo:demo|-|alpha|cto",
		"create|alpha|-|-|-|{json}|cto")
	if err != nil || !reflect.DeepEqual(teamLines(alpha), wantAlpha) {
		t.Fatalf("alpha history: %q, %v; want %q", teamLines(alpha), err, wantAlpha)
	}
	beta, err := s.TeamHistory("beta", 100)
	wantBeta := append(append([]string{}, move...), "create|beta|-|-|-|{json}|cto")
	if err != nil || !reflect.DeepEqual(teamLines(beta), wantBeta) {
		t.Fatalf("beta history: %q, %v; want %q", teamLines(beta), err, wantBeta)
	}
}

// TestBotRevokeIsOneTeamGraphRow: revoking a bot takes it out of its Team with
// one graph_history row, written by the revoke trigger.
func TestBotRevokeIsOneTeamGraphRow(t *testing.T) {
	s, app := botAgentFixture(t)
	bot := protocol.GraphRef{Kind: "agent", ID: app.ID}
	teamWrite(t, s, protocol.TeamWrite{Action: "create", ID: "forge", Name: "forge"})
	teamWrite(t, s, protocol.TeamWrite{Action: "membership", ID: "forge", Member: &bot})
	if err := s.RevokeApp(app.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM graph_history WHERE type='team' AND actor='apps'`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("revoke rows in graph_history: %d, %v; want 1", rows, err)
	}
	h, err := s.TeamHistory("forge", 100)
	want := []string{
		"membership|-|forge|agent:" + app.ID + "|forge|-|apps",
		"membership|forge|-|agent:" + app.ID + "|-|forge|owner",
		"create|forge|-|-|-|{json}|owner",
	}
	if err != nil || !reflect.DeepEqual(teamLines(h), want) {
		t.Fatalf("forge history: %q, %v; want %q", teamLines(h), err, want)
	}
}

// legacyTeamHistory writes the team_history rows the gateway wrote before the
// migration, with the graph_history rows those writes made (a CTO change, a
// lead change, an undo). Each change has its own time.
func legacyTeamHistory(t *testing.T, s *Store) {
	t.Helper()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS team_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			action TEXT NOT NULL,
			team_id TEXT NOT NULL,
			old_team_id TEXT NOT NULL DEFAULT '',
			member_kind TEXT NOT NULL DEFAULT '',
			member_id TEXT NOT NULL DEFAULT '',
			old_value TEXT NOT NULL,
			new_value TEXT NOT NULL,
			at INTEGER NOT NULL,
			actor TEXT NOT NULL)`,
		`INSERT INTO team_history(id,action,team_id,old_team_id,member_kind,member_id,old_value,new_value,at,actor) VALUES
			(1,'create','alpha','','','','','created',1000,'owner'),
			(2,'purpose','alpha','','','','o','x',2000,'cto'),
			(3,'membership','alpha','','repo','demo','','alpha',3000,'owner'),
			(4,'lead','alpha','','','','','demo',4000,'owner'),
			(5,'undo','alpha','','','','x','o',5000,'owner'),
			(6,'membership','','alpha','agent','bot1','alpha','',6000,'apps'),
			(7,'membership','beta','alpha','repo','demo','alpha','beta',7000,'cto'),
			(8,'undo','alpha','beta','repo','demo','beta','alpha',8000,'owner')`,
		`INSERT INTO graph_history(type,node,attr,old_value,new_value,at,actor,target_kind) VALUES
			('team','alpha','purpose','o','x',2000,'cto',''),
			('lead','alpha','lead','','demo',4000,'owner','team'),
			('team','alpha','purpose','x','o',5000,'owner',''),
			('team','beta','membership:repo:demo','alpha','beta',7000,'cto',''),
			('team','beta','membership:repo:demo','beta','alpha',8000,'owner','')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}

// legacyTeamView is the view the old query returned for one Team.
func legacyTeamView(t *testing.T, s *Store, team string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT id,action,team_id,old_team_id,member_kind,member_id,old_value,new_value,at,actor FROM team_history WHERE team_id=? OR old_team_id=? ORDER BY id DESC`, team, team)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var history []protocol.TeamChange
	for rows.Next() {
		var h protocol.TeamChange
		var ref protocol.GraphRef
		if err := rows.Scan(&h.ID, &h.Action, &h.TeamID, &h.OldTeamID, &ref.Kind, &ref.ID, &h.OldValue, &h.NewValue, &h.At, &h.By); err != nil {
			t.Fatal(err)
		}
		if ref.Kind != "" {
			h.Member = &ref
		}
		history = append(history, h)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return teamLines(history)
}

// TestTeamHistoryMigrationCopiesEachLegacyRowOnce migrates a store that holds
// team_history rows. Every change is in graph_history once, the view of each
// Team is the same as before, and a replay of the migration changes nothing.
func TestTeamHistoryMigrationCopiesEachLegacyRowOnce(t *testing.T) {
	s := teamStore(t)
	version := migrationVersion(t, "Team history moves into graph_history")
	legacyTeamHistory(t, s)
	wantAlpha := legacyTeamView(t, s, "alpha")
	wantBeta := legacyTeamView(t, s, "beta")
	if len(wantAlpha) != 8 || len(wantBeta) != 2 {
		t.Fatalf("legacy fixture: alpha %q, beta %q", wantAlpha, wantBeta)
	}
	for run := 0; run < 2; run++ {
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>=?`, version); err != nil {
			t.Fatal(err)
		}
		if err := migrateOldFixture(t, s); err != nil {
			t.Fatalf("migration run %d: %v", run+1, err)
		}
		var tables int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='team_history'`).Scan(&tables); err != nil || tables != 0 {
			t.Fatalf("run %d: team_history tables %d, %v; want dropped", run+1, tables, err)
		}
		var changes, times int
		if err := s.db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT at) FROM graph_history WHERE type IN ('team','team_undo','lead') AND at BETWEEN 1000 AND 8000`).Scan(&changes, &times); err != nil || changes != 8 || times != 8 {
			t.Fatalf("run %d: %d Team rows at %d times, %v; want each of the 8 changes once", run+1, changes, times, err)
		}
		var undos int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM graph_history WHERE type='team_undo' AND at IN (5000,8000)`).Scan(&undos); err != nil || undos != 2 {
			t.Fatalf("run %d: undo rows %d, %v; want 2", run+1, undos, err)
		}
		alpha, err := s.TeamHistory("alpha", 100)
		if err != nil || !reflect.DeepEqual(teamLines(alpha), wantAlpha) {
			t.Fatalf("run %d: alpha history %q, %v; want %q", run+1, teamLines(alpha), err, wantAlpha)
		}
		beta, err := s.TeamHistory("beta", 100)
		if err != nil || !reflect.DeepEqual(teamLines(beta), wantBeta) {
			t.Fatalf("run %d: beta history %q, %v; want %q", run+1, teamLines(beta), err, wantBeta)
		}
	}
}
