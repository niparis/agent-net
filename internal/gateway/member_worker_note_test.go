package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A member can leave "T2b worker running" in its task for an hour after the
// worker ended. The views the CTO and the TUI read add a note at read time.
// The announce itself is never edited.

func TestTaskLineNamingAnEndedWorkerGetsANote(t *testing.T) {
	s, c, member, _ := leadTeamRig(t)
	c.t = time.Date(2026, 11, 9, 1, 30, 0, 0, time.UTC)
	ended := memberWorker(t, s, member, "", time.Date(2026, 11, 9, 1, 7, 0, 0, time.UTC))
	task := "T2b worker " + ended + " running, PR #130 next"
	announce(t, s, member, protocol.ProfileAnnounce{Task: task})
	note := "(worker " + ended + " ended Nov 9 01:07 UTC)"

	a, ok, err := s.Agent(member)
	if err != nil || !ok {
		t.Fatalf("agent: %v %v", ok, err)
	}
	if a.Profile.Task != task {
		t.Fatalf("the announce was edited: %q", a.Profile.Task)
	}
	row := sessionRow(a, false, s.now(), func(id string) string { return id })
	if row.Task != task+" "+note {
		t.Fatalf("session row task = %q, want the task and %q", row.Task, note)
	}
	// The list read carries it too.
	all, err := s.Agents(protocol.QueryAgents{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range all {
		if v.SessionID == member {
			found = true
			if got := sessionRow(v, false, s.now(), func(id string) string { return id }).Task; !strings.HasSuffix(got, note) {
				t.Fatalf("list task = %q", got)
			}
		}
	}
	if !found {
		t.Fatal("member missing from the list")
	}
	// The stored task is the member's own words.
	var stored string
	if err := s.db.QueryRow(`SELECT task FROM profiles WHERE session_id=?`, member).Scan(&stored); err != nil || stored != task {
		t.Fatalf("stored task = %q, %v", stored, err)
	}
}

func TestTaskLineNamingARunningWorkerHasNoNote(t *testing.T) {
	s, _, member, _ := leadTeamRig(t)
	node, _ := s.SessionNode(member)
	running, err := s.UpsertSession(node, protocol.SessionInfo{Harness: "codex", NativeID: "w-run", Worker: &protocol.WorkerInfo{ID: "w_run", Owner: member}})
	if err != nil {
		t.Fatal(err)
	}
	task := "T2b worker " + running + " running"
	announce(t, s, member, protocol.ProfileAnnounce{Task: task})
	a, _, err := s.Agent(member)
	if err != nil {
		t.Fatal(err)
	}
	if got := sessionRow(a, false, s.now(), func(id string) string { return id }).Task; got != task {
		t.Fatalf("task = %q", got)
	}
	// An id of no session at all adds nothing either.
	announce(t, s, member, protocol.ProfileAnnounce{Task: "T2b worker s_0123456789abcdef running"})
	a, _, _ = s.Agent(member)
	if got := sessionRow(a, false, s.now(), func(id string) string { return id }).Task; got != "T2b worker s_0123456789abcdef running" {
		t.Fatalf("unknown worker task = %q", got)
	}
}
