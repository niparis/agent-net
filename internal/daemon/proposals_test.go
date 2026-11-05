package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/niparis/agent-net/internal/protocol"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProposalLifecycle(t *testing.T) {
	t.Parallel()
	s, err := openState(seededStateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	now := time.Unix(100000, 0)
	var calls []string
	p := proposalRunner{state: s, now: func() time.Time { return now }, validate: func(_ context.Context, call proposalCall) error {
		if call.Tool == "bad" {
			return errors.New("bad ID")
		}
		return nil
	}, run: func(_ context.Context, call proposalCall, actor string) (json.RawMessage, error) {
		calls = append(calls, actor)
		if call.Tool == "fail" {
			return nil, errors.New("failed")
		}
		return json.RawMessage(`{"secret":"result-marker"}`), nil
	}}
	ctx := context.Background()
	call := func(name string) proposalCall {
		return proposalCall{Tool: name, Action: "set", Args: json.RawMessage(`{"id":"one"}`)}
	}
	if _, err := p.propose(ctx, call("bad"), ""); err == nil || !strings.Contains(err.Error(), "bad ID") {
		t.Fatalf("bad ID: %v", err)
	}
	id, err := p.propose(ctx, call("ok"), "reason")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.propose(ctx, call("ok"), "another reason"); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := p.decide(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if err := p.decide(ctx, id, true); err == nil {
		t.Fatal("second approval accepted")
	}
	if len(calls) != 1 || calls[0] != "owner (CTO proposal "+id+")" {
		t.Fatalf("calls: %v", calls)
	}
	items, err := p.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || strings.Contains(items[0].Note, "result-marker") || !strings.Contains(string(items[0].Result), "result-marker") {
		t.Fatalf("result isolation: %+v", items)
	}
	id, err = p.propose(ctx, call("decline"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.decide(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	_, err = p.propose(ctx, call("expiry"), "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	items, err = p.list()
	if err != nil {
		t.Fatal(err)
	}
	if items[len(items)-1].Status != "expired" || len(calls) != 1 {
		t.Fatalf("expiry: %+v calls %v", items, calls)
	}
	if items[len(items)-1].Note != items[len(items)-1].ID+" expired" || items[len(items)-2].Note != items[len(items)-2].ID+" declined" {
		t.Fatalf("outcomes: %+v", items)
	}
}

func TestProposalLimitAndApproveAllStops(t *testing.T) {
	t.Parallel()
	s, err := openState(seededStateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	var calls []string
	p := proposalRunner{state: s, now: time.Now, validate: func(_ context.Context, _ proposalCall) error { return nil }, run: func(_ context.Context, c proposalCall, _ string) (json.RawMessage, error) {
		calls = append(calls, c.Tool)
		if c.Tool == "fail" {
			return nil, errors.New("failed")
		}
		return nil, nil
	}}
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("tool-%d", i)
		if i == 1 {
			name = "fail"
		}
		if _, err := p.propose(ctx, proposalCall{Tool: name, Args: json.RawMessage(`{}`)}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.propose(ctx, proposalCall{Tool: "overflow", Args: json.RawMessage(`{}`)}, ""); err == nil {
		t.Fatal("21st accepted")
	}
	if err := p.approveAll(ctx); err == nil {
		t.Fatal("failure ignored")
	}
	if strings.Join(calls, ",") != "tool-0,fail" {
		t.Fatalf("calls: %v", calls)
	}
}

func TestProposalOutcomeDelivery(t *testing.T) {
	s, err := openState(seededStateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	p := proposalRunner{state: s, now: time.Now, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) {
		return json.RawMessage(`{"secret":"result-marker"}`), nil
	}}
	id, err := p.propose(context.Background(), proposalCall{Tool: "one", Args: json.RawMessage(`{}`)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.decide(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	var notes []string
	deliver := func(_ context.Context, note string) error { notes = append(notes, note); return nil }
	if err = p.deliverNote(context.Background(), deliver); err != nil {
		t.Fatal(err)
	}
	if err = p.deliverNote(context.Background(), deliver); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0] != id+" approved and applied" || strings.Contains(notes[0], "result-marker") {
		t.Fatalf("notes: %q", notes)
	}
}

func TestProposalConcurrentOutcomeDelivery(t *testing.T) {
	t.Parallel()
	s, err := openState(seededStateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	p := proposalRunner{state: s, now: time.Now, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) { return nil, nil }}
	id, err := p.propose(context.Background(), proposalCall{Tool: "one", Args: json.RawMessage(`{}`)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.decide(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.deliverNote(context.Background(), func(context.Context, string) error { mu.Lock(); calls++; mu.Unlock(); return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("sent %d times", calls)
	}
}

func TestProposalMessageContextSnapshot(t *testing.T) {
	s, err := openState(seededStateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for i := 0; i < 4; i++ {
		if err := s.recordCTODelivery(fmt.Sprintf("sender-%d", i), time.Unix(int64(100+i), 0)); err != nil {
			t.Fatal(err)
		}
	}
	p := proposalRunner{state: s, now: func() time.Time { return time.Unix(200, 0) }, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) { return nil, nil }}
	_, err = p.propose(context.Background(), proposalCall{Tool: "one", Args: json.RawMessage(`{}`)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.recordCTODelivery("later", time.Unix(201, 0)); err != nil {
		t.Fatal(err)
	}
	items, err := p.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].Messages) != 3 || items[0].Messages[0].Sender != "sender-1" || items[0].Messages[2].Sender != "sender-3" {
		t.Fatalf("messages: %+v", items)
	}
}

func TestProposalWaitsForCTOIdle(t *testing.T) {
	f := startCTOCompactionFixture(t)
	f.setActive(t, true)
	p := proposalRunner{state: f.n.d.st, now: time.Now, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) {
		return json.RawMessage(`{"data":"result-marker"}`), nil
	}}
	id, err := p.propose(context.Background(), proposalCall{Tool: "one", Args: json.RawMessage(`{}`)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.decide(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	if err = f.n.d.deliverProposalNote(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := readFake(t, filepath.Join(f.n.cfg.StateDir, "cto"))
	if strings.Contains(strings.Join(before.Turns, " "), id) {
		t.Fatalf("sent while busy: %v", before.Turns)
	}
	f.setActive(t, false)
	if err = f.n.d.deliverProposalNote(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := readFake(t, filepath.Join(f.n.cfg.StateDir, "cto"))
	if got := after.Turns[len(after.Turns)-1]; got != id+" approved and applied" || strings.Contains(got, "result-marker") {
		t.Fatalf("note: %q", got)
	}
}

func TestProposalTUIAcknowledgment(t *testing.T) {
	for _, tc := range []struct {
		name          string
		started       bool
		message, want string
	}{{"applied", true, "", "applied"}, {"failed", false, "could not start", "failed"}} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := openState(seededStateDir(t))
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			p := proposalRunner{state: s, now: time.Now, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) {
				return json.RawMessage(`{"id":"start_test","status":"handed_to_tui"}`), nil
			}}
			id, err := p.propose(context.Background(), proposalCall{Tool: "session_change", Action: "session_start", Args: json.RawMessage(`{}`)}, "")
			if err != nil {
				t.Fatal(err)
			}
			if err = p.decide(context.Background(), id, true); err != nil {
				t.Fatal(err)
			}
			items, _ := p.list()
			if items[0].Status != "handed_to_tui" {
				t.Fatalf("status %s", items[0].Status)
			}
			if err = p.ackTUI(context.Background(), id, tc.started, tc.message); err != nil {
				t.Fatal(err)
			}
			items, _ = p.list()
			if items[0].Status != tc.want || (!tc.started && !strings.Contains(items[0].Note, tc.message)) {
				t.Fatalf("proposal %+v", items[0])
			}
		})
	}
}

func TestProposalTUITimeout(t *testing.T) {
	s, err := openState(seededStateDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	p := proposalRunner{state: s, now: time.Now, validate: func(context.Context, proposalCall) error { return nil }, run: func(context.Context, proposalCall, string) (json.RawMessage, error) {
		return json.RawMessage(`{"id":"start_test","status":"handed_to_tui"}`), nil
	}}
	id, err := p.propose(context.Background(), proposalCall{Tool: "session_change", Action: "session_start", Args: json.RawMessage(`{}`)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.decide(context.Background(), id, true); err != nil {
		t.Fatal(err)
	}
	if err = p.timeoutTUI(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	items, _ := p.list()
	if items[0].Status != "failed" || !strings.Contains(items[0].Note, "the approving TUI did not confirm the start") {
		t.Fatalf("proposal %+v", items[0])
	}
}

func TestProposalHandoffFailsOnDaemonRestart(t *testing.T) {
	dir := seededStateDir(t)
	st, err := openState(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.db.Exec(`INSERT INTO cto_proposals(tool,action,args,reason,created_at,status,note,result,note_delivered) VALUES ('session_change','session_start','{}','',1,'handed_to_tui','sent','{"id":"start_test","status":"handed_to_tui"}',1),('x','y','{}','',1,'applied','kept','{}',0)`)
	if err != nil {
		t.Fatal(err)
	}
	st.close()
	d, err := New(Config{StateDir: dir, SocketPath: filepath.Join(t.TempDir(), "daemon.sock"), DetectHarnesses: fakeInventory})
	if err != nil {
		t.Fatal(err)
	}
	defer d.st.close()
	items, err := d.proposalRunner().list()
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "failed" || !strings.Contains(items[0].Note, "the daemon restarted before the TUI confirmed the start") || !strings.Contains(string(items[0].Result), `"status":"failed"`) || !strings.Contains(string(items[0].Result), "the daemon restarted before the TUI confirmed the start") || items[1].Status != "applied" {
		t.Fatalf("proposals %+v", items)
	}
	var delivered int
	if err := d.st.db.QueryRow(`SELECT note_delivered FROM cto_proposals WHERE id=1`).Scan(&delivered); err != nil || delivered != 0 {
		t.Fatalf("note delivered %d: %v", delivered, err)
	}
}

func TestProposalLateTUIAckLogged(t *testing.T) {
	for _, started := range []bool{true, false} {
		t.Run(fmt.Sprint(started), func(t *testing.T) {
			s, err := openState(seededStateDir(t))
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			_, err = s.db.Exec(`INSERT INTO cto_proposals(tool,action,args,reason,created_at,status,note,result) VALUES ('session_change','session_start','{}','',1,'failed','timeout','{}')`)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			d := &Daemon{st: s, log: log.New(&buf, "", 0)}
			lc := &localConn{d: d}
			raw, _ := json.Marshal(protocol.CTOStartAck{ID: "p_1", Started: started})
			_, _, err = lc.dispatchOwner(context.Background(), protocol.OpProposalStartAck, raw)
			if err == nil || !strings.Contains(err.Error(), "not awaiting a TUI start") {
				t.Fatalf("error %v", err)
			}
			if lines := strings.Split(strings.TrimSpace(buf.String()), "\n"); len(lines) != 1 || !strings.Contains(lines[0], "p_1") || !strings.Contains(lines[0], fmt.Sprintf("started=%t", started)) {
				t.Fatalf("log %q", buf.String())
			}
		})
	}
}
