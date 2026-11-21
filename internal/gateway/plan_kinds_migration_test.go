package gateway

import (
	"testing"
)

// queryLines returns one string per row: the columns joined with "|".
func queryLines(t *testing.T, s *Store, query string) []string {
	t.Helper()
	rows, err := s.db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		out = append(out, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func equalLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d rows %q, want %d rows %q", what, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s row %d: got %q, want %q", what, i, got[i], want[i])
		}
	}
}

// The plan tables and the OAuth rate-limit tables were merged into one table
// each, with a kind. The merge keeps every row and drops the old tables. A
// rewind that replays the merge copies nothing and does not fail.
func TestPlanAndAttemptTablesMergeWithKinds(t *testing.T) {
	s, _ := newTestStore(t)
	rewind := migrationVersion(t, "One table for the plans that change tools") - 1
	// The tools retire migration drops plans later, so the replay stops at the merge.
	replayToMerge := func() error {
		t.Helper()
		return replayMigrationsUpTo(t, s, rewind+1)
	}
	rewindToMerge := func() {
		t.Helper()
		if _, err := s.db.Exec(`DELETE FROM schema_version WHERE version>?`, rewind); err != nil {
			t.Fatal(err)
		}
	}

	// The shipped tables, as the migrations before the merge made them.
	rewindToMerge()
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS tool_plans (id TEXT PRIMARY KEY, item TEXT NOT NULL, snapshot TEXT NOT NULL, plan TEXT NOT NULL, applied INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS mcp_import_plans (id TEXT PRIMARY KEY, snapshot TEXT NOT NULL, plan TEXT NOT NULL, applied INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS mcp_native_plans (id TEXT PRIMARY KEY, snapshot TEXT NOT NULL, plan TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'pending')`,
		`CREATE TABLE IF NOT EXISTS oauth_code_misses (source TEXT NOT NULL, attempted_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS oauth_registration_attempts (source TEXT NOT NULL, created_at INTEGER NOT NULL)`,
	} {
		if _, err := s.db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`DELETE FROM tool_plans`, `DELETE FROM mcp_import_plans`, `DELETE FROM mcp_native_plans`,
		`DELETE FROM oauth_code_misses`, `DELETE FROM oauth_registration_attempts`,
		`INSERT INTO tool_plans(id,item,snapshot,plan,applied) VALUES('plan_tool_1','item-a','snap-tool','{"operation":"skill_group"}',1)`,
		`INSERT INTO mcp_import_plans(id,snapshot,plan,applied) VALUES('plan_import_1','snap-import','{"candidate":"import"}',0)`,
		`INSERT INTO mcp_native_plans(id,snapshot,plan,state) VALUES('plan_native_1','snap-native','{"candidate":"native"}','approved')`,
		`INSERT INTO oauth_code_misses(source,attempted_at) VALUES('one',100),('two',200)`,
		`INSERT INTO oauth_registration_attempts(source,created_at) VALUES('192.0.2.1',300)`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	wantPlans := []string{
		`mcp_import|plan_import_1||snap-import|{"candidate":"import"}|0|`,
		`mcp_native|plan_native_1||snap-native|{"candidate":"native"}|0|approved`,
		`tool|plan_tool_1|item-a|snap-tool|{"operation":"skill_group"}|1|`,
	}
	wantAttempts := []string{"code_miss|one|100", "code_miss|two|200", "registration|192.0.2.1|300"}
	const plansQuery = `SELECT kind||'|'||id||'|'||item||'|'||snapshot||'|'||plan||'|'||applied||'|'||state FROM plans ORDER BY kind,id`
	const attemptsQuery = `SELECT kind||'|'||source||'|'||at FROM oauth_attempts ORDER BY kind,source,at`

	if err := replayToMerge(); err != nil {
		t.Fatal(err)
	}
	equalLines(t, "plans after the merge", queryLines(t, s, plansQuery), wantPlans)
	equalLines(t, "attempts after the merge", queryLines(t, s, attemptsQuery), wantAttempts)
	var left int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('tool_plans','mcp_import_plans','mcp_native_plans','oauth_code_misses','oauth_registration_attempts')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("old tables left after the merge: %d (%v)", left, err)
	}

	// Replay: the tables are gone, and the merge must still run without an error or a copy.
	rewindToMerge()
	if err := replayToMerge(); err != nil {
		t.Fatalf("replay of the merge: %v", err)
	}
	equalLines(t, "plans after the replay", queryLines(t, s, plansQuery), wantPlans)
	equalLines(t, "attempts after the replay", queryLines(t, s, attemptsQuery), wantAttempts)
}
