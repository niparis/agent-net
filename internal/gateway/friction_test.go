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

// Friction never scores anyone: the picker's inputs, the calibration and the
// defect counts are the same before and after rows are stored.
func TestFrictionDoesNotChangePickerOrWorkerCounts(t *testing.T) {
	s, _ := newTestStore(t)
	beforePicker := s.pickerTable()
	beforeDefects, err := s.DefectCountsBySession(0)
	if err != nil {
		t.Fatal(err)
	}
	beforeCalibration, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
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
	afterDefects, err := s.DefectCountsBySession(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterDefects, beforeDefects) {
		t.Fatalf("defect counts changed: %+v -> %+v", beforeDefects, afterDefects)
	}
	afterCalibration, err := s.Calibration()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterCalibration, beforeCalibration) {
		t.Fatal("calibration changed after a friction report")
	}
	afterScores, err := s.scores()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterScores, beforeScores) {
		t.Fatalf("worker ratings changed: %+v -> %+v", beforeScores, afterScores)
	}
}
