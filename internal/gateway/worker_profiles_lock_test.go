package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// Worker profile edits and stored messages both need the one writer
// connection. An edit that held a lock a message needed, or the reverse, hung
// the whole store. The edits and the stores run together here and must finish
// before the deadline. A hang never finishes, so the deadline only sets how long
// a failing run waits. It is long because a race build runs an edit in about
// 0.1 s, and a loaded machine can be slower than that.
func TestWorkerProfileEditsDoNotWaitOnStoredMessages(t *testing.T) {
	s, _ := newTestStore(t)
	a, b := twoSessions(t, s)
	disableInboxLimit(t, s)

	stop := make(chan struct{})
	storeDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				storeDone <- nil
				return
			default:
			}
			_, _, err := s.StoreMessage(protocol.MsgSend{Sender: a, Recipient: b, Type: protocol.TypeNotification, Body: "fyi"})
			// Past the rate limit every store is refused, but still runs its transaction.
			if err != nil && !strings.Contains(err.Error(), "rate limit") {
				storeDone <- err
				return
			}
		}
	}()

	edits := make(chan error, 1)
	go func() { edits <- editWorkerRoster(s, 10) }()
	select {
	case err := <-edits:
		close(stop)
		if err != nil {
			t.Fatalf("worker profile edits: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("worker profile edits did not finish in 15s: the store is waiting on a lock")
	}
	if err := <-storeDone; err != nil {
		t.Fatalf("stored message: %v", err)
	}
}

// editWorkerRoster adds n profiles from two writers at once, checks the roster,
// then removes every profile and checks it again. Two writers at once would
// lose a profile if the read and the write of an edit could interleave.
func editWorkerRoster(s *Store, n int) error {
	name := func(i int) string { return fmt.Sprintf("opencode fake/m%d", i) }
	errs := make(chan error, 2)
	for w := 0; w < 2; w++ {
		go func(w int) {
			var err error
			for i := w; i < n && err == nil; i += 2 {
				err = s.AddWorkerProfile(protocol.WorkerProfile{Name: name(i), Harness: "opencode", Model: fmt.Sprintf("fake/m%d", i), AddedBy: "s_test", AddedAt: 123})
			}
			errs <- err
		}(w)
	}
	for w := 0; w < 2; w++ {
		if err := <-errs; err != nil {
			return err
		}
	}
	profiles, err := s.WorkerProfiles()
	if err != nil {
		return err
	}
	if len(profiles) != n {
		return fmt.Errorf("roster has %d profiles after two writers added %d", len(profiles), n)
	}
	for i := 0; i < n; i++ {
		if err := s.RemoveWorkerProfile(name(i)); err != nil {
			return err
		}
	}
	profiles, err = s.WorkerProfiles()
	if err != nil {
		return err
	}
	if len(profiles) != 0 {
		return fmt.Errorf("roster has %d profiles after removing all", len(profiles))
	}
	return nil
}
