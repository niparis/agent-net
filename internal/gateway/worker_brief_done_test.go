package gateway

import (
	"slices"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

// The done list of the start is stored with the change, and the accept and
// review tasks check exactly that list, so grading equals the brief.
func TestWorkerBriefDoneReachesTheChangeAndStepTasks(t *testing.T) {
	r := newDeliveryRig(t)
	if err := r.s.AddArchitect("acc", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := r.s.SetEdge(protocol.Edge{From: "acc", To: "demo", Kind: "accepts"}, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.UpsertSession(r.b, protocol.SessionInfo{Harness: "pi", NativeID: "acc", Repo: protocol.RepoInfo{Project: "acc"}}); err != nil {
		t.Fatal(err)
	}
	done := []string{"the failing test passes", "no new warnings"}
	if err := r.s.trackWorkerStart("q_done", r.a, r.caller, "demo", "claude", "sandboxed", done); err != nil {
		t.Fatal(err)
	}
	if err := r.s.startSession(r.a, "q_done", r.worker); err != nil {
		t.Fatal(err)
	}
	q := r.workerSubmit("agent-net/w_1", commitA)
	q.Difficulty = "hard"
	c := r.submit(r.a, q).Change.Change
	if !slices.Equal(c.Done, done) {
		t.Fatalf("change done = %#v, want %#v", c.Done, done)
	}
	if got := r.get(c.ID).Change.Done; !slices.Equal(got, done) {
		t.Fatalf("stored done = %#v, want %#v", got, done)
	}
	rows, err := r.s.db.Query(`SELECT body FROM messages WHERE body LIKE '%definition of done (check each):%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var bodies []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// One accept task and one review task, for their two owners.
	if len(bodies) != 2 {
		t.Fatalf("%d step tasks carry the done list, want 2: %#v", len(bodies), bodies)
	}
	for _, body := range bodies {
		for _, want := range done {
			if !strings.Contains(body, "- "+want) {
				t.Fatalf("step task lacks %q:\n%s", want, body)
			}
		}
	}
}
