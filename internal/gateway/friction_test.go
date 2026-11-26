package gateway

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Decision 0170: a worker may paste part of a command line into a friction
// line. The text is redacted before it reaches the table.
func TestFrictionTextIsRedactedBeforeStorage(t *testing.T) {
	s, _ := newTestStore(t)
	const secret = "sk-abcdefghijklmnopqrstuvwx"
	res, err := s.ReportFriction(protocol.FrictionReportRequest{
		SessionID: "s_1", Project: "demo", Harness: "opencode", Model: "m",
		Entries: []protocol.FrictionEntry{{Area: protocol.FrictionTooling, Text: "the proxy asked for " + secret}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stored != 1 {
		t.Fatalf("stored %d rows", res.Stored)
	}
	var text string
	if err := s.db.QueryRow(`SELECT text FROM friction`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, secret) {
		t.Fatalf("the secret is in the table: %q", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("the redaction mark is missing: %q", text)
	}
}

// A hand-back's friction report is optional, so the gateway cleans a bad
// entry of a report that reaches it instead of refusing the whole list.
func TestFrictionReportDropsBadEntries(t *testing.T) {
	s, _ := newTestStore(t)
	res, err := s.ReportFriction(protocol.FrictionReportRequest{
		SessionID: "s_1", Project: "demo", Harness: "agy",
		Entries: []protocol.FrictionEntry{
			{Area: protocol.FrictionTooling, Text: "the fake agy took two runs"},
			{Area: "", Text: ""},
			{Area: protocol.FrictionCI, Text: "the suite ran\ntwice"},
		},
	})
	if err != nil {
		t.Fatalf("the report was refused for a bad entry: %v", err)
	}
	if res.Stored != 2 {
		t.Fatalf("stored %d rows", res.Stored)
	}
	rows, err := s.db.Query(`SELECT area, text FROM friction ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var areas, texts []string
	for rows.Next() {
		var area, text string
		if err := rows.Scan(&area, &text); err != nil {
			t.Fatal(err)
		}
		areas, texts = append(areas, area), append(texts, text)
	}
	if len(areas) != 2 || areas[0] != protocol.FrictionTooling || areas[1] != protocol.FrictionCI {
		t.Fatalf("stored areas: %v", areas)
	}
	if texts[1] != "the suite ran twice" {
		t.Fatalf("a wrapped line was not made one line: %q", texts[1])
	}
}

// The read filters, returns the newest first, and counts each project.
func TestFrictionFindFiltersAndCounts(t *testing.T) {
	s, c := newTestStore(t)
	entries := []protocol.FrictionReportRequest{
		{SessionID: "s_1", Project: "demo", Harness: "opencode", Model: "m1", Entries: []protocol.FrictionEntry{{Area: protocol.FrictionCI, Text: "the suite flaked"}}},
		{SessionID: "s_2", Project: "demo", Harness: "claude", Entries: []protocol.FrictionEntry{{Area: protocol.FrictionTooling, Text: "the hook was missing"}}},
		{SessionID: "s_3", Project: "other", Harness: "pi", Entries: []protocol.FrictionEntry{{Area: protocol.FrictionCI, Text: "the runner was full"}}},
	}
	for i, q := range entries {
		c.advance(time.Minute)
		if _, err := s.ReportFriction(q); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	got, err := s.FrictionFind(protocol.FrictionQuery{Project: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 2 || got.Total != 2 {
		t.Fatalf("demo rows: %+v", got)
	}
	if got.Entries[0].Session != "s_2" {
		t.Fatalf("newest first: %+v", got.Entries)
	}
	if !strings.HasPrefix(got.Entries[0].Text, `"the hook was missing"`) {
		t.Fatalf("text is not quoted data: %q", got.Entries[0].Text)
	}
	if !reflect.DeepEqual(got.Counts, []protocol.FrictionProjectCount{{Project: "demo", Count: 2, Unread: 2}}) {
		t.Fatalf("counts: %+v", got.Counts)
	}
	ci, err := s.FrictionFind(protocol.FrictionQuery{Area: protocol.FrictionCI})
	if err != nil {
		t.Fatal(err)
	}
	if len(ci.Entries) != 2 || ci.Counts[0].Project != "demo" {
		t.Fatalf("area filter: %+v", ci)
	}
}

// Friction never scores anyone: the picker's inputs are the same before and
// after rows are stored.
func TestFrictionDoesNotChangePickerOrWorkerCounts(t *testing.T) {
	s, _ := newTestStore(t)
	beforePicker := s.pickerTable()
	if _, err := s.db.Exec(`INSERT INTO worker_ratings(session_id,caller,score,at) VALUES('s_rated','s_caller',5,?)`, s.now().Unix()); err != nil {
		t.Fatal(err)
	}
	beforeScores, err := s.scores()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReportFriction(protocol.FrictionReportRequest{SessionID: "s_1", Project: "demo",
		Entries: []protocol.FrictionEntry{{Area: protocol.FrictionReview, Text: "the review took two days"}}}); err != nil {
		t.Fatal(err)
	}
	if got := s.pickerTable(); !reflect.DeepEqual(got, beforePicker) {
		t.Fatalf("picker rows changed: %+v -> %+v", beforePicker, got)
	}
	afterScores, err := s.scores()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterScores, beforeScores) {
		t.Fatalf("worker ratings changed: %+v -> %+v", beforeScores, afterScores)
	}
}

// The weekly digest reads the friction table and takes rows whose digest_id is
// empty. The standalone tool sends this same request through ReportFriction, so
// its report is a row the next digest carries, with the group and new status
// the digest uses.
func TestFrictionReportIsReadyForTheDigest(t *testing.T) {
	s, _ := newTestStore(t)
	const text = "the sandbox cannot bind a local port"
	if _, err := s.ReportFriction(protocol.FrictionReportRequest{
		SessionID: "s_1", Project: "demo", ChangeID: "ch_1234", Harness: "claude", Model: "m",
		Entries: []protocol.FrictionEntry{{Area: protocol.FrictionEnvironment, Text: text}},
	}); err != nil {
		t.Fatal(err)
	}
	var digest string
	if err := s.db.QueryRow(`SELECT digest_id FROM friction`).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if digest != "" {
		t.Fatalf("digest_id = %q, want empty so the next digest carries the report", digest)
	}
	got, err := s.FrictionFind(protocol.FrictionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("rows: %+v", got.Entries)
	}
	if e := got.Entries[0]; e.Group != protocol.FrictionGroup(protocol.FrictionEnvironment, text) || e.Status != protocol.FrictionNew {
		t.Fatalf("digest fields: group %q, status %q", e.Group, e.Status)
	}
}

// FrictionFind's main page read must use a covering index on friction and
// never do a full table scan of friction.
func TestFrictionFindUsesCoveringIndex(t *testing.T) {
	s, _ := newTestStore(t)
	for _, tc := range []struct {
		name    string
		project string
		area    string
		status  string
	}{
		{name: "all"},
		{name: "project", project: "demo"},
		{name: "area", area: protocol.FrictionCI},
		{name: "status", status: protocol.FrictionNew},
	} {
		t.Run(tc.name, func(t *testing.T) {
			where, args := frictionFilter(tc.project, tc.area, tc.status, "", 0, 0)
			q := `EXPLAIN QUERY PLAN SELECT ` + frictionCols + frictionFrom + where + ` ORDER BY f.id DESC LIMIT ?`
			rows, err := s.db.Query(q, append(args, frictionDefaultLimit+1)...)
			if err != nil {
				t.Fatal(err)
			}
			var plan []string
			for rows.Next() {
				var id, parent, notused int
				var detail string
				if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			rows.Close()
			joined := strings.Join(plan, "\n")
			for _, d := range plan {
				if d == "SCAN f" || strings.HasPrefix(d, "SCAN friction") {
					t.Fatalf("main query does a full scan of friction:\n%s", joined)
				}
			}
			if !strings.Contains(joined, "USING COVERING INDEX friction_") {
				t.Fatalf("main query does not use a covering index on friction:\n%s", joined)
			}
		})
	}
}
