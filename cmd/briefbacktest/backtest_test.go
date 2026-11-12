package main

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// passingBrief has every item of the live format, in the form the live check
// reads: a Goal that names the owner, a path, a Limits section, the two labels,
// and an Evidence section with three lines.
const passingBrief = `Goal: asked by the owner in the agent-net lead session ("replay the brief checks").
Files or areas: cmd/briefbacktest/rules.go
Limits:
- Tests use temp dirs and a database copy only; never the real setup.
Definition of done: the replay tool committed.
How you are graded: the caller reruns the tool on a fresh copy.
Evidence:
- one
- two
- three
`

func TestStrictRulesPassAFullBrief(t *testing.T) {
	failed, unmapped := strictRules(passingBrief)
	if len(failed) != 0 || len(unmapped) != 0 {
		t.Fatalf("passing brief failed %v, unmapped %v", failed, unmapped)
	}
}

// Each mutation removes one item. Exactly that rule must fail.
func TestStrictRulesFailOnlyTheMissingItem(t *testing.T) {
	cases := []struct {
		name string
		old  string
		new  string
		want []string
	}{
		{"goal", `Goal: asked by the owner in the agent-net lead session ("replay the brief checks").`, "Goal: make the tool.", []string{ruleGoal}},
		{"files", "cmd/briefbacktest/rules.go", "the rules file", []string{ruleFiles}},
		{"limits", "Limits:\n- Tests use temp dirs and a database copy only; never the real setup.\n", "", []string{ruleLimits}},
		{"done", "Definition of done: the replay tool committed.\n", "", []string{ruleDone}},
		{"graded", "How you are graded: the caller reruns the tool on a fresh copy.\n", "", []string{ruleGraded}},
		{"evidence short", "Evidence:\n- one\n- two\n- three", "Evidence:\n- one", []string{ruleEvidence}},
		{"evidence missing", "Evidence:\n- one\n- two\n- three\n", "", []string{ruleEvidence}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(passingBrief, tc.old) {
				t.Fatalf("test setup: %q is not in the passing brief", tc.old)
			}
			task := strings.Replace(passingBrief, tc.old, tc.new, 1)
			failed, unmapped := strictRules(task)
			if !reflect.DeepEqual(failed, tc.want) {
				t.Fatalf("failed rules = %v, want %v", failed, tc.want)
			}
			if len(unmapped) != 0 {
				t.Fatalf("unmapped failure lines: %v", unmapped)
			}
		})
	}
}

func TestFailedRulesKeepsUnmappedLines(t *testing.T) {
	failed, unmapped := failedRules([]string{
		"overlaps running worker s_x on internal/x.go",
		"Evidence: at least 3 lines (found 1)",
	})
	if !reflect.DeepEqual(failed, []string{ruleEvidence}) {
		t.Fatalf("failed = %v", failed)
	}
	if !reflect.DeepEqual(unmapped, []string{"overlaps running worker s_x on internal/x.go"}) {
		t.Fatalf("unmapped = %v", unmapped)
	}
}

// A brief that states its content in prose, with no format headings, fails the
// live check. The looser matchers should release the rules it meets in prose.
const proseBrief = `The lead asked for a replay of the brief checks on past briefs.
Files: cmd/briefbacktest/rules.go is the place.
Do not touch the real setup; run only in temp dirs.
Done when the table is in the report and the tool is committed.
It is graded by the lead on the two numbers.
Found in the copy: three facts we saw.
- first
- second
- third
`

func TestLooseRulesReleaseProseBriefs(t *testing.T) {
	strict, _ := strictRules(proseBrief)
	want := []string{ruleGoal, ruleDone, ruleEvidence}
	if !reflect.DeepEqual(strict, want) {
		t.Fatalf("strict failed = %v, want %v", strict, want)
	}
	if loose := looseRules(proseBrief, strict); len(loose) != 0 {
		t.Fatalf("loose still failed %v, want none", loose)
	}
}

// Files has no looser matcher. Without a path, files stays failed even when
// the prose matchers release every other rule.
func TestLooseRulesNeverReleaseFiles(t *testing.T) {
	task := strings.Replace(proseBrief, "cmd/briefbacktest/rules.go", "the rules file", 1)
	strict, _ := strictRules(task)
	if !reflect.DeepEqual(strict, []string{ruleGoal, ruleFiles, ruleDone, ruleEvidence}) {
		t.Fatalf("strict failed = %v", strict)
	}
	if loose := looseRules(task, strict); !reflect.DeepEqual(loose, []string{ruleFiles}) {
		t.Fatalf("loose failed = %v, want only files", loose)
	}
}

func TestBriefTaskIsTextAfterTheMarker(t *testing.T) {
	task, ok := briefTask("[agent-net worker brief] preamble\n\nTask:\nGoal: x\n")
	if !ok || task != "Goal: x\n" {
		t.Fatalf("briefTask = %q, %v", task, ok)
	}
	if _, ok := briefTask("[agent-net worker brief] no marker here"); ok {
		t.Fatal("a message without the marker must not parse")
	}
}

// fixtureDB builds the four tables the replay reads, with the same column
// names as the gateway, in a temporary file.
func fixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	schema := []string{
		`CREATE TABLE messages (id TEXT PRIMARY KEY, sender TEXT NOT NULL, recipient TEXT NOT NULL, created_at INTEGER NOT NULL, body TEXT NOT NULL)`,
		`CREATE TABLE worker_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL,
			ended_at INTEGER NOT NULL DEFAULT 0, early_quit INTEGER NOT NULL DEFAULT 0, fix_count INTEGER NOT NULL DEFAULT 0,
			continue_count INTEGER NOT NULL DEFAULT 0, struggle_failed INTEGER NOT NULL DEFAULT 0, struggle_repeats INTEGER NOT NULL DEFAULT 0,
			struggle_retries INTEGER NOT NULL DEFAULT 0, struggle_exploration INTEGER NOT NULL DEFAULT 0, struggle_env INTEGER NOT NULL DEFAULT 0,
			worker_complete INTEGER, difficulty_used TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE worker_ratings (session_id TEXT NOT NULL UNIQUE, score INTEGER NOT NULL)`,
		`CREATE TABLE worker_defects (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, severity INTEGER NOT NULL DEFAULT 1)`,
		`CREATE TABLE worker_brief_rejections (id INTEGER PRIMARY KEY AUTOINCREMENT, failures TEXT NOT NULL DEFAULT '')`,
	}
	for _, s := range schema {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestRunReplayJoinsBriefsToOutcomes(t *testing.T) {
	db := fixtureDB(t)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	const brief = "[agent-net worker brief] preamble\n\nTask:\n"
	// m1: a passing brief whose worker completed with a 5 and no fix round.
	exec(`INSERT INTO messages VALUES ('m1','caller','s1',1,?)`, brief+passingBrief)
	exec(`INSERT INTO worker_runs (session_id, outcome, ended_at, worker_complete, difficulty_used) VALUES ('s1','complete',10,1,'normal')`)
	exec(`INSERT INTO worker_ratings VALUES ('s1',5)`)
	// m2: a prose brief with two fix rounds, a rating of 2 and a defect.
	exec(`INSERT INTO messages VALUES ('m2','caller','s2',2,?)`, brief+proseBrief)
	exec(`INSERT INTO worker_runs (session_id, outcome, ended_at, fix_count) VALUES ('s2','partial',10,1)`)
	exec(`INSERT INTO worker_runs (session_id, outcome, ended_at, fix_count, continue_count) VALUES ('s2','complete',20,1,1)`)
	exec(`INSERT INTO worker_ratings VALUES ('s2',2)`)
	exec(`INSERT INTO worker_defects (session_id, severity) VALUES ('s2',2)`)
	// m3: never launched, so it has no runs and no outcome.
	exec(`INSERT INTO messages VALUES ('m3','caller','s3',3,?)`, brief+"Goal: x\n")
	// m4: no task marker, so it is counted and skipped.
	exec(`INSERT INTO messages VALUES ('m4','caller','s4',4,'[agent-net worker brief] no marker')`)
	exec(`INSERT INTO worker_brief_rejections (failures) VALUES (?)`,
		"Definition of done: add this line\noverlaps open change ch_1 on x/y.go")

	res, err := runReplay(db)
	if err != nil {
		t.Fatal(err)
	}
	if res.NoMarker != 1 || len(res.Briefs) != 3 {
		t.Fatalf("no marker = %d, briefs = %d; want 1 and 3", res.NoMarker, len(res.Briefs))
	}
	m1, m2, m3 := res.Briefs[0], res.Briefs[1], res.Briefs[2]
	if !m1.Outcome.Launched || m1.Outcome.Bad() || len(m1.Strict) != 0 {
		t.Fatalf("m1 = %+v", m1.Outcome)
	}
	if m1.Outcome.Difficulty != "normal" || m2.Outcome.Difficulty != "" {
		t.Fatalf("difficulty m1=%q m2=%q", m1.Outcome.Difficulty, m2.Outcome.Difficulty)
	}
	if m2.Outcome.FixRounds != 2 || m2.Outcome.Continues != 1 || m2.Outcome.Final != "complete" || !m2.Outcome.Bad() {
		t.Fatalf("m2 outcome = %+v", m2.Outcome)
	}
	if !reflect.DeepEqual(m2.Strict, []string{ruleGoal, ruleDone, ruleEvidence}) {
		t.Fatalf("m2 strict = %v", m2.Strict)
	}
	if m3.Outcome.Launched || m3.Outcome.Bad() {
		t.Fatalf("m3 must not be launched: %+v", m3.Outcome)
	}
	if res.Rejections.Rows != 1 || res.Rejections.ByRule[ruleDone] != 1 || res.Rejections.Unmapped != 1 {
		t.Fatalf("rejections = %+v", res.Rejections)
	}

	report := renderReport(res, "fixture.db")
	if !strings.Contains(report, "| Brief messages replayed | 3 |") {
		t.Fatalf("report does not state the replayed count:\n%s", report)
	}
	if !strings.Contains(report, "| Briefs with a launched worker (joined to runs) | 2 |") {
		t.Fatalf("report does not state the launched count:\n%s", report)
	}

	var buf bytes.Buffer
	if err := writeCSV(&buf, res.Briefs); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0][0] != "brief_id" || rows[2][16] != "yes" {
		t.Fatalf("csv rows = %d, row m2 bad flag = %v", len(rows), rows[2])
	}
}

// scoredFor builds one launched brief from one caller. Bad is a fix round.
func scoredFor(sender string, refused, bad bool) scored {
	s := scored{brief: brief{Sender: sender}, Outcome: outcome{Launched: true, Final: "complete"}}
	if refused {
		s.Strict = []string{ruleGoal}
	}
	if bad {
		s.Outcome.FixRounds = 1
	}
	return s
}

// Caller A: refused bad 1 of 2, passed bad 0 of 2, gives 0.5 with weight 1.
// Caller B: refused bad 1 of 1, passed bad 1 of 1, gives 0 with weight 0.5.
// Caller C has refused briefs only, so it adds to the counts but not to the estimate.
func TestStratifiedBadPoolsCallersWithBothGroups(t *testing.T) {
	briefs := []scored{
		scoredFor("A", true, true), scoredFor("A", true, false),
		scoredFor("A", false, false), scoredFor("A", false, false),
		scoredFor("B", true, true), scoredFor("B", false, true),
		scoredFor("C", true, true),
	}
	rd, strata, nRef, nPass := stratifiedBy(briefs, func(s scored) bool { return refusedBy(s.Strict, ruleGoal) },
		func(s scored) string { return s.Sender })
	if strata != 2 || nRef != 4 || nPass != 3 {
		t.Fatalf("strata=%d refused=%d passed=%d; want 2, 4, 3", strata, nRef, nPass)
	}
	if want := 1.0 / 3.0; rd < want-1e-9 || rd > want+1e-9 {
		t.Fatalf("risk difference = %v, want %v", rd, want)
	}
}
