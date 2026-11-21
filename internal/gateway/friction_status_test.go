package gateway

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// reportFriction stores one friction line from one session.
func reportFriction(t *testing.T, s *Store, project, session, area, text string) {
	t.Helper()
	if _, err := s.ReportFriction(protocol.FrictionReportRequest{SessionID: session, Project: project, Harness: "claude",
		Entries: []protocol.FrictionEntry{{Area: area, Text: text}}}); err != nil {
		t.Fatal(err)
	}
}

// Decision 0175: a mark sets the status of a report. The link and note are the
// last mark's: a mark without them clears them.
func TestFrictionMarkSetsStatus(t *testing.T) {
	s, c := newTestStore(t)
	reportFriction(t, s, "demo", "s_1", protocol.FrictionCI, "the suite flaked")
	c.advance(time.Minute)
	before, err := s.FrictionFind(protocol.FrictionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	id := before.Entries[0].ID
	if before.Entries[0].Status != protocol.FrictionNew || before.Unread != 1 {
		t.Fatalf("a new report: status %q, unread %d", before.Entries[0].Status, before.Unread)
	}
	if _, err := s.MarkFriction("cto", protocol.FrictionMarkRequest{IDs: []int64{id}, Status: protocol.FrictionRead, Link: "ch_1234", Note: "seen"}); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	if _, err := s.MarkFriction("owner", protocol.FrictionMarkRequest{IDs: []int64{id}, Status: protocol.FrictionFixed}); err != nil {
		t.Fatal(err)
	}
	got, err := s.FrictionFind(protocol.FrictionQuery{ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("one report by id: %+v", got.Entries)
	}
	e := got.Entries[0]
	if e.Status != protocol.FrictionFixed || e.ChangedBy != "owner" || e.Link != "" || e.Note != "" {
		t.Fatalf("current state after the second mark: %+v", e)
	}
	if got.Unread != 0 {
		t.Fatalf("unread after a mark: %d", got.Unread)
	}
}

// A page holds at most the limit and names the cursor of the next page. Walking
// the cursor reads every report once, so none is hidden by a row cap.
func TestFrictionFindPagesWithoutHidingRows(t *testing.T) {
	s, _ := newTestStore(t)
	const n = 45
	for i := 0; i < n; i++ {
		reportFriction(t, s, "demo", fmt.Sprintf("s_%d", i), protocol.FrictionCI, fmt.Sprintf("flake number %d", i))
	}
	seen := map[int64]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := s.FrictionFind(protocol.FrictionQuery{Limit: 20, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if pages > 5 {
			t.Fatal("paging does not end")
		}
		if page.Total != n {
			t.Fatalf("total on page %d = %d, want %d", pages, page.Total, n)
		}
		for _, e := range page.Entries {
			if seen[e.ID] {
				t.Fatalf("report %d on two pages", e.ID)
			}
			seen[e.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != n || pages != 3 {
		t.Fatalf("read %d reports in %d pages, want %d in 3", len(seen), pages, n)
	}
}

// Total, unread and the per-project counts cover every report the filter holds,
// not only the page.
func TestFrictionFindCountsUnreadPerProject(t *testing.T) {
	s, _ := newTestStore(t)
	reportFriction(t, s, "demo", "s_1", protocol.FrictionCI, "alpha one")
	reportFriction(t, s, "demo", "s_2", protocol.FrictionTooling, "beta two")
	reportFriction(t, s, "demo", "s_3", protocol.FrictionReview, "gamma three")
	reportFriction(t, s, "other", "s_4", protocol.FrictionCI, "delta four")
	demo, err := s.FrictionFind(protocol.FrictionQuery{Project: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkFriction("cto", protocol.FrictionMarkRequest{IDs: []int64{demo.Entries[2].ID}, Status: protocol.FrictionRead}); err != nil {
		t.Fatal(err)
	}
	got, err := s.FrictionFind(protocol.FrictionQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 4 || got.Unread != 3 || len(got.Entries) != 1 {
		t.Fatalf("total %d, unread %d, page %d", got.Total, got.Unread, len(got.Entries))
	}
	if len(got.Counts) != 2 || got.Counts[0].Project != "demo" || got.Counts[0].Count != 3 || got.Counts[0].Unread != 2 {
		t.Fatalf("counts: %+v", got.Counts)
	}
	if got.Counts[1].Project != "other" || got.Counts[1].Unread != 1 {
		t.Fatalf("other project counts: %+v", got.Counts[1])
	}
}

// Reports that say the same thing share a group, so one mark by group closes
// all of them and leaves the others alone.
func TestFrictionGroupMarksAllDuplicates(t *testing.T) {
	s, _ := newTestStore(t)
	reportFriction(t, s, "demo", "s_1", protocol.FrictionCI, "the suite flaked on run 12 in /w/a")
	reportFriction(t, s, "other", "s_2", protocol.FrictionCI, "the suite flaked on run 99 in /w/b")
	reportFriction(t, s, "demo", "s_3", protocol.FrictionCI, "the review took two days")
	all, err := s.FrictionFind(protocol.FrictionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var group, reviewGroup string
	for _, e := range all.Entries {
		if e.Text == `"the review took two days"` {
			reviewGroup = e.Group
			continue
		}
		if group != "" && e.Group != group {
			t.Fatalf("duplicates have groups %q and %q", group, e.Group)
		}
		group = e.Group
	}
	if group == "" || reviewGroup == "" {
		t.Fatalf("missing group ids: duplicates %q, review %q", group, reviewGroup)
	}
	if reviewGroup == group {
		t.Fatal("a different text shares the group")
	}
	members, err := s.FrictionFind(protocol.FrictionQuery{Group: group})
	if err != nil {
		t.Fatal(err)
	}
	if len(members.Entries) != 2 || members.Total != 2 {
		t.Fatalf("group members: %+v", members.Entries)
	}
	res, err := s.MarkFriction("owner", protocol.FrictionMarkRequest{Group: group, Status: protocol.FrictionFixed})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 2 || res.Marked != 2 {
		t.Fatalf("group mark: %+v", res)
	}
	fixed, err := s.FrictionFind(protocol.FrictionQuery{Status: protocol.FrictionFixed})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.Total != 2 {
		t.Fatalf("fixed reports after the group mark: %d", fixed.Total)
	}
	open, err := s.FrictionFind(protocol.FrictionQuery{Status: protocol.FrictionNew})
	if err != nil {
		t.Fatal(err)
	}
	if open.Total != 1 || open.Entries[0].Text != `"the review took two days"` {
		t.Fatalf("the report outside the group changed: %+v", open.Entries)
	}
}

// A filter mark counts its matches first. A dry run writes nothing, and ids that
// do not exist are not counted or written.
func TestFrictionMarkByFilterDryRunWritesNothing(t *testing.T) {
	s, _ := newTestStore(t)
	reportFriction(t, s, "demo", "s_1", protocol.FrictionCI, "the suite flaked")
	reportFriction(t, s, "demo", "s_2", protocol.FrictionCI, "the runner flaked twice")
	reportFriction(t, s, "demo", "s_3", protocol.FrictionTooling, "the hook was missing")
	filter := protocol.FrictionMarkRequest{Area: protocol.FrictionCI, Text: "FLAKED", Status: protocol.FrictionWontfix}
	dry := filter
	dry.DryRun = true
	res, err := s.MarkFriction("cto", dry)
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Matched != 2 || res.Marked != 0 {
		t.Fatalf("dry run: %+v", res)
	}
	if got, _ := s.FrictionFind(protocol.FrictionQuery{Status: protocol.FrictionWontfix}); got.Total != 0 {
		t.Fatalf("a dry run wrote %d marks", got.Total)
	}
	res, err = s.MarkFriction("cto", filter)
	if err != nil {
		t.Fatal(err)
	}
	if res.DryRun || res.Matched != 2 || res.Marked != 2 || len(res.SampleIDs) != 2 {
		t.Fatalf("filter mark: %+v", res)
	}
	if got, _ := s.FrictionFind(protocol.FrictionQuery{Status: protocol.FrictionWontfix}); got.Total != 2 {
		t.Fatalf("filter mark wrote %d marks, want 2", got.Total)
	}
	res, err = s.MarkFriction("cto", protocol.FrictionMarkRequest{IDs: []int64{9999}, Status: protocol.FrictionRead})
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 0 || res.Marked != 0 {
		t.Fatalf("an unknown id matched: %+v", res)
	}
}

// A database written before this migration reads its reports as new, and a mark
// works on them after the upgrade.
func TestFrictionStatusUpgradesFromPreviousSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "previous.db")
	old := migrateOnly(t, path, len(migrations)-1)
	reportFriction(t, old, "demo", "s_1", protocol.FrictionCI, "the suite flaked")
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := openStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	got, err := upgraded.FrictionFind(protocol.FrictionQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Status != protocol.FrictionNew || got.Unread != 1 {
		t.Fatalf("an old report after the upgrade: %+v", got)
	}
	if _, err := upgraded.MarkFriction("owner", protocol.FrictionMarkRequest{IDs: []int64{got.Entries[0].ID}, Status: protocol.FrictionRead}); err != nil {
		t.Fatal(err)
	}
}
