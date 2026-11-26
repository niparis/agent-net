package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// pausedLoop makes the proj loop paused the way two ticks with no progress do.
func pausedLoop(t *testing.T, s *Store, c *clock) string {
	t.Helper()
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	if _, err := s.db.Exec(`UPDATE agent_loops SET paused=?,streak=? WHERE node='proj'`, loopNoMoving, protocol.LoopNoProgressStop); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLoopResumeOnOwnerWordsRestartsTheClock(t *testing.T) {
	s, c := newTestStore(t)
	pausedLoop(t, s, c)
	res, err := s.ResumeLoopOnOwnerWords(protocol.LoopResumeFrame{Node: "proj", By: "cto", OwnerWords: "resume the loop", WordsVerified: true})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Node != "proj" || res.Status != "resumed" {
		t.Fatalf("result: %+v", res)
	}
	var paused, streak, started int
	if err := s.db.QueryRow(`SELECT COALESCE(paused,0),COALESCE(streak,0),COALESCE(started_at,0) FROM agent_loops WHERE node='proj'`).Scan(&paused, &streak, &started); err != nil {
		t.Fatal(err)
	}
	if paused != 0 || streak != 0 || started != 0 {
		t.Fatalf("loop after resume paused=%d streak=%d started=%d", paused, streak, started)
	}
	// The clock starts again at once, as the Desk resume did.
	res1 := loopTicks(t, s)
	if len(res1) != 1 || res1[0].Outcome != protocol.LoopSent {
		t.Fatalf("tick after resume = %+v", res1)
	}
}

func TestLoopResumeNeedsVerifiedOwnerWords(t *testing.T) {
	s, c := newTestStore(t)
	pausedLoop(t, s, c)
	for _, frame := range []protocol.LoopResumeFrame{
		{Node: "proj", WordsVerified: true},
		{Node: "proj", OwnerWords: "resume the loop"},
	} {
		if _, err := s.ResumeLoopOnOwnerWords(frame); err == nil {
			t.Fatalf("resume without verified words was accepted: %+v", frame)
		}
	}
	if _, err := s.ResumeLoopOnOwnerWords(protocol.LoopResumeFrame{Node: "missing", OwnerWords: "resume it", WordsVerified: true}); err == nil || !strings.Contains(err.Error(), "unknown loop") {
		t.Fatalf("unknown loop: %v", err)
	}
}

func TestLoopPauseLineNamesLoopResume(t *testing.T) {
	s, c := newTestStore(t)
	_, b := twoSessions(t, s)
	loopNode(t, s, c, b)
	loopTicks(t, s)
	for i := 0; i < 3; i++ {
		idleSince(t, s, c, b, time.Minute)
		runLoops(t, s, c)
	}
	var paused int
	if err := s.db.QueryRow(`SELECT COALESCE(paused,0) FROM agent_loops WHERE node='proj'`).Scan(&paused); err != nil || paused != loopNoMoving {
		t.Fatalf("paused = %d, %v", paused, err)
	}
	line := ""
	for _, l := range deskLines(t, s) {
		if strings.Contains(l, "Loop proj: paused") {
			line = l
		}
	}
	if !strings.Contains(line, "loop_resume") || !strings.Contains(line, `"node":"proj"`) {
		t.Fatalf("pause line does not name the CTO call: %q", line)
	}
}
