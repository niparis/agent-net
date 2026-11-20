package gateway

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Part 6 of the schema reduction (decision 0179): eight side tables went into
// their parents and friction_status went away. These tests build the layout
// that a shipped gateway has, run the migration on it, and check that every
// row and every reading is the same as before.

func sideFoldExec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func sideFoldInt(t *testing.T, s *Store, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func sideFoldString(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	var v string
	if err := s.db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

// sideFoldBase is an hour before the clock these tests use, in nanoseconds.
// Model marks are compared with a window that ends at the clock, so they sit
// inside it.
var sideFoldBase = time.Date(2026, 11, 20, 11, 0, 0, 0, time.UTC).UnixNano()

// sideFoldMigrationIndex is the version of the fold migration.
func sideFoldMigrationIndex(t *testing.T) int {
	t.Helper()
	for i, m := range migrations {
		if strings.Contains(m, sideFoldMarker) {
			return i + 1
		}
	}
	t.Fatal("the fold migration is not in the list")
	return 0
}

// oldSideTableLayout turns a migrated store back into the layout before the
// fold: the side tables with their rows and none of the new columns. It
// returns the path of the store, closed and marked as not yet migrated.
func oldSideTableLayout(t *testing.T) (path string, friction []int64) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "old.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s.now = (&clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}).now

	// Friction reports and marks come first: they need the current layout.
	for _, text := range []string{"marked twice", "status row without history", "never marked"} {
		reportFriction(t, s, "demo", "s_1", protocol.FrictionCI, text)
	}
	found, err := s.FrictionFind(protocol.FrictionQuery{})
	if err != nil || len(found.Entries) != 3 {
		t.Fatalf("reports: %+v %v", found, err)
	}
	// Entries come newest first.
	twice, orphan, never := found.Entries[2].ID, found.Entries[1].ID, found.Entries[0].ID
	friction = []int64{twice, orphan, never}
	for _, status := range []string{protocol.FrictionRead, protocol.FrictionTriaged} {
		if _, err := s.MarkFriction("cto", protocol.FrictionMarkRequest{IDs: []int64{twice}, Status: status, Link: "ch_1", Note: "n"}); err != nil {
			t.Fatal(err)
		}
	}

	// Drop what the fold added, then bring the old tables back.
	for _, q := range []string{
		`DROP INDEX worker_defects_auto_key`,
		`ALTER TABLE learned_suggestions DROP COLUMN origin`, `ALTER TABLE learned_suggestions DROP COLUMN history_id`, `ALTER TABLE learned_suggestions DROP COLUMN raw_phrase`,
		`ALTER TABLE session_wakes DROP COLUMN wake_failed_at`, `ALTER TABLE session_wakes DROP COLUMN wake_failed_reason`, `ALTER TABLE session_wakes DROP COLUMN wake_attempted_at`,
		`ALTER TABLE worker_defects DROP COLUMN auto_change_id`, `ALTER TABLE worker_defects DROP COLUMN auto_kind`, `ALTER TABLE worker_defects DROP COLUMN auto_revision`,
		`ALTER TABLE model_failures DROP COLUMN success_at`,
		`ALTER TABLE member_blocked_notices DROP COLUMN tick_hash`,
		`CREATE TABLE learned_suggestion_details (suggestion_id INTEGER PRIMARY KEY, origin TEXT NOT NULL DEFAULT '', history_id INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE learned_phrase_state (suggestion_id INTEGER PRIMARY KEY, raw_phrase TEXT NOT NULL)`,
		`CREATE TABLE session_wake_failures (session_id TEXT PRIMARY KEY, failed_at INTEGER NOT NULL, reason TEXT NOT NULL)`,
		`CREATE TABLE retired_session_wakes (session_id TEXT PRIMARY KEY, attempted_at INTEGER NOT NULL)`,
		`CREATE TABLE worker_defect_auto (defect_id INTEGER PRIMARY KEY, change_id TEXT NOT NULL, kind TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0, at INTEGER NOT NULL)`,
		`CREATE UNIQUE INDEX worker_defect_auto_key ON worker_defect_auto(change_id, kind, revision)`,
		`CREATE TABLE model_successes (node_id TEXT NOT NULL, harness TEXT NOT NULL, model TEXT NOT NULL, at INTEGER NOT NULL, PRIMARY KEY (node_id, harness, model))`,
		`CREATE TABLE member_blocked_ticks (session_id TEXT PRIMARY KEY, text_hash TEXT NOT NULL)`,
		`CREATE TABLE friction_status (friction_id INTEGER PRIMARY KEY, status TEXT NOT NULL, changed_by TEXT NOT NULL DEFAULT '', changed_at INTEGER NOT NULL, link TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '')`,
	} {
		sideFoldExec(t, s, q)
	}

	// Learned: one with both side rows, one with details only, one with none.
	for i, phrase := range []string{"cleaned one", "two", "three"} {
		sideFoldExec(t, s, `INSERT INTO learned_suggestions(node,field,edge_kind,kind,phrase,evidence,status,severity,created_at,ready_at) VALUES('app','rules','','add',?,'[]','pending','minor',1,1)`, phrase)
		_ = i
	}
	sideFoldExec(t, s, `INSERT INTO learned_suggestion_details(suggestion_id,origin,history_id) VALUES(1,'owner',7),(2,'flow',0)`)
	sideFoldExec(t, s, `INSERT INTO learned_phrase_state(suggestion_id,raw_phrase) VALUES(1,'raw one')`)

	// Wakes: s1 has a wake count, a failure and an attempt. s2 has a failure
	// and an attempt but no count. s3 has only an attempt.
	sideFoldExec(t, s, `INSERT INTO session_wakes(session_id,wake_day,wake_day_count,wake_streak) VALUES('s1','2026-11-20',3,2)`)
	sideFoldExec(t, s, `INSERT INTO session_wake_failures(session_id,failed_at,reason) VALUES('s1',111,'refused'),('s2',222,'')`)
	sideFoldExec(t, s, `INSERT INTO retired_session_wakes(session_id,attempted_at) VALUES('s1',100),('s2',200),('s3',300)`)

	// Defects: one by hand, two recorded by agent-net.
	sideFoldExec(t, s, `INSERT INTO worker_defects(session_id,severity,at) VALUES('w1',2,10),('w1',3,20),('w1',4,30)`)
	sideFoldExec(t, s, `INSERT INTO worker_defect_auto(defect_id,change_id,kind,revision,at) VALUES(2,'ch_a','verify',1,20),(3,'ch_a','test',2,30)`)

	// Models: both marks, a failure only, a success only.
	sideFoldExec(t, s, `INSERT INTO model_failures(node_id,harness,model,at,auth_at) VALUES('n1','claude','both',?,?),('n1','claude','fail-only',?,0)`, sideFoldBase+50, sideFoldBase+60, sideFoldBase+70)
	sideFoldExec(t, s, `INSERT INTO model_successes(node_id,harness,model,at) VALUES('n1','claude','both',?),('n1','claude','ok-only',?)`, sideFoldBase+80, sideFoldBase+90)

	// Blocked members: a notice and a tick, a tick only, a notice only.
	sideFoldExec(t, s, `INSERT INTO member_blocked_notices(session_id,text_hash,at) VALUES('m1','notice-1',5),('m3','notice-3',6)`)
	sideFoldExec(t, s, `INSERT INTO member_blocked_ticks(session_id,text_hash) VALUES('m1','tick-1'),('m2','tick-2')`)

	// The status table agrees with the history for one report, and a second
	// report has a row there and no history at all.
	sideFoldExec(t, s, `INSERT INTO friction_status(friction_id,status,changed_by,changed_at,link,note)
		SELECT friction_id,status,changed_by,changed_at,link,note FROM friction_status_history WHERE id=(SELECT MAX(id) FROM friction_status_history WHERE friction_id=?)`, twice)
	sideFoldExec(t, s, `INSERT INTO friction_status(friction_id,status,changed_by,changed_at,link,note) VALUES(?,?,?,?,?,?)`, orphan, protocol.FrictionFixed, "owner", 999, "ch_2", "by hand")

	sideFoldExec(t, s, `DELETE FROM schema_version WHERE version >= ?`, sideFoldMigrationIndex(t))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path, friction
}

func TestSideFoldMigrationKeepsEveryRow(t *testing.T) {
	path, friction := oldSideTableLayout(t)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, gone := range []string{"learned_suggestion_details", "learned_phrase_state", "session_wake_failures", "retired_session_wakes",
		"worker_defect_auto", "model_successes", "member_blocked_ticks", "friction_status"} {
		if n := sideFoldInt(t, s, `SELECT COUNT(*) FROM sqlite_master WHERE name=?`, gone); n != 0 {
			t.Errorf("%s is still there", gone)
		}
	}

	t.Run("learned", func(t *testing.T) {
		for _, c := range []struct {
			id      int
			origin  string
			history int64
			raw     string
			hasRaw  bool
		}{{1, "owner", 7, "raw one", true}, {2, "flow", 0, "", false}, {3, "", 0, "", false}} {
			var origin string
			var history int64
			var raw *string
			if err := s.db.QueryRow(`SELECT origin,history_id,raw_phrase FROM learned_suggestions WHERE id=?`, c.id).Scan(&origin, &history, &raw); err != nil {
				t.Fatal(err)
			}
			if origin != c.origin || history != c.history || (raw != nil) != c.hasRaw || (raw != nil && *raw != c.raw) {
				t.Errorf("suggestion %d: origin %q history %d raw %v", c.id, origin, history, raw)
			}
		}
	})

	t.Run("session wakes", func(t *testing.T) {
		// s1 keeps its count and gains both marks. s2 and s3 get a row.
		if n := sideFoldInt(t, s, `SELECT wake_day_count FROM session_wakes WHERE session_id='s1'`); n != 3 {
			t.Errorf("s1 count %d", n)
		}
		for _, c := range []struct {
			session  string
			failed   int64
			reason   string
			attempts int64
		}{{"s1", 111, "refused", 100}, {"s2", 222, "", 200}, {"s3", 0, "", 300}} {
			var failed, attempted int64
			var reason string
			if err := s.db.QueryRow(`SELECT wake_failed_at,wake_failed_reason,wake_attempted_at FROM session_wakes WHERE session_id=?`, c.session).Scan(&failed, &reason, &attempted); err != nil {
				t.Fatalf("%s: %v", c.session, err)
			}
			if failed != c.failed || reason != c.reason || attempted != c.attempts {
				t.Errorf("%s: failed %d %q attempted %d", c.session, failed, reason, attempted)
			}
		}
		if reason, ok := s.wakeFailure("s2"); !ok || reason != "" {
			t.Errorf("a failure with no reason reads %q, %v", reason, ok)
		}
	})

	t.Run("defects", func(t *testing.T) {
		for _, c := range []struct {
			id       int
			change   *string
			kind     string
			revision int
		}{{1, nil, "", 0}, {2, ptr("ch_a"), "verify", 1}, {3, ptr("ch_a"), "test", 2}} {
			var change, kind *string
			var revision *int
			if err := s.db.QueryRow(`SELECT auto_change_id,auto_kind,auto_revision FROM worker_defects WHERE id=?`, c.id).Scan(&change, &kind, &revision); err != nil {
				t.Fatal(err)
			}
			if (change == nil) != (c.change == nil) || (change != nil && (*change != *c.change || *kind != c.kind || *revision != c.revision)) {
				t.Errorf("defect %d: %v %v %v", c.id, change, kind, revision)
			}
		}
		// The same change, kind and revision cannot be recorded twice, and
		// any number of defects by hand can stand.
		if _, err := s.db.Exec(`INSERT INTO worker_defects(session_id,at,auto_change_id,auto_kind,auto_revision) VALUES('w1',1,'ch_a','verify',1)`); err == nil {
			t.Error("a second automatic defect for the same change, kind and revision was stored")
		}
		sideFoldExec(t, s, `INSERT INTO worker_defects(session_id,at) VALUES('w1',1),('w1',2)`)
	})

	t.Run("models", func(t *testing.T) {
		s.now = (&clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}).now
		ok, err := s.SucceededModels("n1", "claude")
		if err != nil || len(ok) != 2 || ok["both"].UnixNano() != sideFoldBase+80 || ok["ok-only"].UnixNano() != sideFoldBase+90 {
			t.Errorf("succeeded: %v %v", ok, err)
		}
		failed, err := s.FailedModels("n1", "claude")
		if err != nil || len(failed) != 2 || failed["both"].UnixNano() != sideFoldBase+50 || failed["fail-only"].UnixNano() != sideFoldBase+70 {
			t.Errorf("failed: %v %v", failed, err)
		}
		if _, there := failed["ok-only"]; there {
			t.Error("a model that only succeeded reads as failed")
		}
		// The auth failure of "both" came before its success.
		if blocked, err := s.ModelAuthBlocked("n1", "claude", "both"); err != nil || blocked {
			t.Errorf("auth blocked after a later success: %v %v", blocked, err)
		}
	})

	t.Run("blocked members", func(t *testing.T) {
		for _, c := range []struct{ session, notice, tick string }{{"m1", "notice-1", "tick-1"}, {"m2", "", "tick-2"}, {"m3", "notice-3", ""}} {
			var notice, tick string
			if err := s.db.QueryRow(`SELECT text_hash,tick_hash FROM member_blocked_notices WHERE session_id=?`, c.session).Scan(&notice, &tick); err != nil {
				t.Fatalf("%s: %v", c.session, err)
			}
			if notice != c.notice || tick != c.tick {
				t.Errorf("%s: notice %q tick %q", c.session, notice, tick)
			}
		}
	})

	t.Run("friction", func(t *testing.T) {
		twice, orphan, never := friction[0], friction[1], friction[2]
		got, err := s.FrictionFind(protocol.FrictionQuery{ID: twice})
		if err != nil || len(got.Entries) != 1 {
			t.Fatalf("find: %+v %v", got, err)
		}
		if e := got.Entries[0]; e.Status != protocol.FrictionTriaged || e.ChangedBy != "cto" || e.Link != "ch_1" || len(e.History) != 2 {
			t.Errorf("a report marked twice: %+v", e)
		}
		got, err = s.FrictionFind(protocol.FrictionQuery{ID: orphan})
		if err != nil || len(got.Entries) != 1 {
			t.Fatalf("find: %+v %v", got, err)
		}
		// The status table said fixed. The history now says it too.
		if e := got.Entries[0]; e.Status != protocol.FrictionFixed || e.ChangedBy != "owner" || e.ChangedAt != 999 || e.Link != "ch_2" || e.Note != "by hand" || len(e.History) != 1 {
			t.Errorf("a status row with no history: %+v", e)
		}
		got, err = s.FrictionFind(protocol.FrictionQuery{ID: never})
		if err != nil || len(got.Entries) != 1 || got.Entries[0].Status != protocol.FrictionNew || len(got.Entries[0].History) != 0 {
			t.Errorf("a report nobody marked: %+v %v", got, err)
		}
		all, err := s.FrictionFind(protocol.FrictionQuery{})
		if err != nil || all.Total != 3 || all.Unread != 1 {
			t.Errorf("counts: total %d unread %d %v", all.Total, all.Unread, err)
		}
		// A mark on the migrated store is the latest, and a filter sees it.
		if _, err := s.MarkFriction("owner", protocol.FrictionMarkRequest{IDs: []int64{never}, Status: protocol.FrictionWontfix}); err != nil {
			t.Fatal(err)
		}
		by, err := s.FrictionFind(protocol.FrictionQuery{Status: protocol.FrictionWontfix})
		if err != nil || len(by.Entries) != 1 || by.Entries[0].ID != never {
			t.Errorf("filter by status: %+v %v", by, err)
		}
	})
}

func ptr(s string) *string { return &s }

// A rewind can replay the migration after a later one ran, and the old tables
// can come back empty from an earlier migration. The replay must neither fail
// nor lose a row.
func TestSideFoldMigrationReplays(t *testing.T) {
	path, _ := oldSideTableLayout(t)
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// An older migration made the side tables again, empty.
	sideFoldExec(t, s, `CREATE TABLE IF NOT EXISTS learned_suggestion_details (suggestion_id INTEGER PRIMARY KEY, origin TEXT NOT NULL DEFAULT '', history_id INTEGER NOT NULL DEFAULT 0)`)
	sideFoldExec(t, s, `CREATE TABLE IF NOT EXISTS friction_status (friction_id INTEGER PRIMARY KEY, status TEXT NOT NULL, changed_by TEXT NOT NULL DEFAULT '', changed_at INTEGER NOT NULL, link TEXT NOT NULL DEFAULT '', note TEXT NOT NULL DEFAULT '')`)
	history := sideFoldInt(t, s, `SELECT COUNT(*) FROM friction_status_history`)
	sideFoldExec(t, s, `DELETE FROM schema_version WHERE version >= ?`, sideFoldMigrationIndex(t))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := sideFoldInt(t, s, `SELECT history_id FROM learned_suggestions WHERE id=1`); n != 7 {
		t.Errorf("history id %d after a replay", n)
	}
	if n := sideFoldInt(t, s, `SELECT COUNT(*) FROM friction_status_history`); n != history {
		t.Errorf("history rows %d, want %d", n, history)
	}
}

// The wake back-off is the WHERE of the upsert: a second claim inside the
// back-off is refused and leaves the row alone, and a claim after it passes.
// A session with a wake count keeps the count through claims and failures.
func TestSideFoldWakeBackoffLivesOnTheWakeRow(t *testing.T) {
	s, c := newTestStore(t)
	sideFoldExec(t, s, `INSERT INTO session_wakes(session_id,wake_day,wake_day_count) VALUES('s1','2026-11-20',4)`)
	ok, err := s.claimWake("s1")
	if err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	first := sideFoldInt(t, s, `SELECT wake_attempted_at FROM session_wakes WHERE session_id='s1'`)
	c.advance(wakeBackoff - time.Second)
	if ok, err := s.claimWake("s1"); err != nil || ok {
		t.Fatalf("a claim inside the back-off: %v %v", ok, err)
	}
	if n := sideFoldInt(t, s, `SELECT wake_attempted_at FROM session_wakes WHERE session_id='s1'`); n != first {
		t.Errorf("a refused claim moved the attempt time: %d, was %d", n, first)
	}
	c.advance(2 * time.Second)
	if ok, err := s.claimWake("s1"); err != nil || !ok {
		t.Fatalf("a claim after the back-off: %v %v", ok, err)
	}
	// A refused wake and its clearing leave the count and the attempt alone.
	if _, err := s.failWake("s1", "a1", "refused"); err != nil {
		t.Fatal(err)
	}
	if reason, ok := s.wakeFailure("s1"); !ok || reason != "refused" {
		t.Fatalf("failure: %q %v", reason, ok)
	}
	if err := s.clearWakeFailure("s1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.wakeFailure("s1"); ok {
		t.Error("the failure stands after it was cleared")
	}
	if n := sideFoldInt(t, s, `SELECT wake_day_count FROM session_wakes WHERE session_id='s1'`); n != 4 {
		t.Errorf("the count changed to %d", n)
	}
	if n := sideFoldInt(t, s, `SELECT wake_attempted_at FROM session_wakes WHERE session_id='s1'`); n == 0 {
		t.Error("the attempt time was lost")
	}
	// A wake count written after a claim does not clear the attempt either.
	sideFoldExec(t, s, `INSERT INTO session_wakes(session_id,wake_day,wake_day_count) VALUES('s1','2026-11-21',1)
		ON CONFLICT(session_id) DO UPDATE SET wake_day=excluded.wake_day,wake_day_count=excluded.wake_day_count`)
	if ok, err := s.claimWake("s1"); err != nil || ok {
		t.Errorf("a claim right after another: %v %v", ok, err)
	}
}

// One row holds what a lead's tick listed and what the notice told. Clearing
// one keeps the other, and a row with neither goes.
func TestSideFoldMemberTickAndNoticeShareARow(t *testing.T) {
	s, _ := newTestStore(t)
	rows := func() int64 {
		return sideFoldInt(t, s, `SELECT COUNT(*) FROM member_blocked_notices WHERE session_id='m1'`)
	}
	if err := s.recordMemberTold(s.db, []memberTold{{session: "m1", key: "k1"}}); err != nil {
		t.Fatal(err)
	}
	if got := sideFoldString(t, s, `SELECT tick_hash FROM member_blocked_notices WHERE session_id='m1'`); got != "k1" {
		t.Fatalf("tick %q", got)
	}
	// A tick alone is no notice.
	if got := sideFoldString(t, s, `SELECT text_hash FROM member_blocked_notices WHERE session_id='m1'`); got != "" {
		t.Fatalf("notice %q", got)
	}
	if err := clearMemberTick(s.db, "m1"); err != nil || rows() != 0 {
		t.Fatalf("a tick-only row after clearing: %d rows, %v", rows(), err)
	}
	sideFoldExec(t, s, `INSERT INTO member_blocked_notices(session_id,text_hash,at) VALUES('m1','n',1)`)
	if err := s.recordMemberTold(s.db, []memberTold{{session: "m1", key: "k2"}}); err != nil {
		t.Fatal(err)
	}
	if err := clearMemberTick(s.db, "m1"); err != nil {
		t.Fatal(err)
	}
	if got := sideFoldString(t, s, `SELECT text_hash||'/'||tick_hash FROM member_blocked_notices WHERE session_id='m1'`); got != "n/" {
		t.Errorf("after clearing the tick: %q, want the notice kept", got)
	}
}

// A model that only succeeded is not a failure, a failure after a success
// keeps the success, and the success window still applies.
func TestSideFoldModelSuccessLivesNextToFailure(t *testing.T) {
	s, c := newTestStore(t)
	if err := s.RecordModelSuccess("n1", "claude", "m"); err != nil {
		t.Fatal(err)
	}
	failed, err := s.FailedModels("n1", "claude")
	if err != nil || len(failed) != 0 {
		t.Fatalf("a success read as a failure: %v %v", failed, err)
	}
	c.advance(time.Minute)
	if err := s.RecordModelFailure("n1", "claude", "m"); err != nil {
		t.Fatal(err)
	}
	ok, err := s.SucceededModels("n1", "claude")
	if err != nil || len(ok) != 1 {
		t.Fatalf("a failure erased the success: %v %v", ok, err)
	}
	failed, err = s.FailedModels("n1", "claude")
	if err != nil || len(failed) != 1 {
		t.Fatalf("the failure is missing: %v %v", failed, err)
	}
	c.advance(time.Duration(protocol.ModelSuccessDays)*24*time.Hour + time.Hour)
	ok, err = s.SucceededModels("n1", "claude")
	if err != nil || len(ok) != 0 {
		t.Fatalf("an old success counts: %v %v", ok, err)
	}
}
