package daemon

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A queued start keeps the picker's why and its family filters across the
// database, so the started notice of a dequeued start still explains the pick.
func TestQueuedStartKeepsWhyInTheStartedNotice(t *testing.T) {
	st, err := openState(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.close() })
	why := "difficulty hard (D 1.90): deepseek agy 0.62 (n4); 1 qualifying"
	q := queuedStart{ID: "q_1", Owner: "s_1", Project: "p", Harness: "opencode", Model: "m",
		Task: "t", Why: why, NotFamily: []string{"google"}, QueuedAt: time.Now()}
	if err := st.addQueued(q); err != nil {
		t.Fatal(err)
	}
	rows, err := st.queue()
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %+v %v", rows, err)
	}
	got := rows[0]
	if got.Why != why {
		t.Errorf("queued why = %q, want %q", got.Why, why)
	}
	if !reflect.DeepEqual(got.NotFamily, []string{"google"}) {
		t.Errorf("queued not_family = %v", got.NotFamily)
	}
	// The started notice is built from the row read back from the database.
	body := protocol.WorkerStartedText("s_w", got.Harness, got.Model, got.Project, "host", 0, got.Why)
	if !strings.Contains(body, "\nwhy: "+why+"\n") {
		t.Errorf("started notice lacks the why line:\n%s", body)
	}
}
