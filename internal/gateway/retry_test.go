package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/coder/websocket"
	"testing"
	"time"
)

func TestRetryRule(t *testing.T) {
	for _, tc := range []struct {
		detail string
		want   bool
	}{
		{"no such local session", true}, {"Pi worker extension is disconnected", true},
		{"Claude delivery broken by Claude Code 1", true}, {"inbox socket refused: missing", true},
		{"write failed after connecting; the message may have arrived", false},
		{"only the worker's owner can start it again", false}, {"plugin refused: policy", false},
		{"expired", false}, {"unknown failure", false},
	} {
		t.Run(tc.detail, func(t *testing.T) {
			if got := retryableFailure(tc.detail); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
func TestRetryBackoff(t *testing.T) {
	for i, want := range []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, 30 * time.Minute} {
		if got := retryDelay(i + 1); got != want {
			t.Fatalf("attempt %d: %s want %s", i+1, got, want)
		}
	}
}

func TestRetrySchedulePersistsAndStops(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, err := send(s, a, b, "retry", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyDelivery(m.ID, "failed", "no such local session"); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var next int64
	read := func() {
		t.Helper()
		if err := s.db.QueryRow(`SELECT attempts,next_at FROM delivery_retries WHERE message_id=?`, m.ID).Scan(&attempts, &next); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if attempts != 1 || next != c.t.Add(30*time.Second).UnixNano() {
		t.Fatalf("schedule %d %v", attempts, time.Unix(0, next))
	}
	var path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(new(int), new(string), &path); err != nil {
		t.Fatal(err)
	}
	other, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.db.QueryRow(`SELECT attempts,next_at FROM delivery_retries WHERE message_id=?`, m.ID).Scan(&attempts, &next); err != nil || attempts != 1 {
		t.Fatalf("reopened schedule: %d %v", attempts, err)
	}
	if _, err := s.db.Exec(`UPDATE messages SET state='dispatched' WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyDelivery(m.ID, "failed", "no such local session"); err != nil {
		t.Fatal(err)
	}
	read()
	if attempts != 2 || next != c.t.Add(2*time.Minute).UnixNano() {
		t.Fatalf("second schedule %d %v", attempts, time.Unix(0, next))
	}
	if _, err := s.db.Exec(`UPDATE messages SET state='dispatched' WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyDelivery(m.ID, "delivered", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT attempts FROM delivery_retries WHERE message_id=?`, m.ID).Scan(&attempts); err == nil {
		t.Fatal("success kept schedule")
	}
}

func TestRetryUncertainAndExpiry(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	m, _, err := send(s, a, b, "uncertain", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDelivery(m.ID, "failed", "write failed; the message may have arrived"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM delivery_retries WHERE message_id=?`, m.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("uncertain scheduled: %d %v", count, err)
	}
	n, _, err := send(s, a, b, "expire", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDelivery(n.ID, "failed", "no such local session"); err != nil {
		t.Fatal(err)
	}
	c.t = n.ExpiresAt.Add(time.Second)
	if _, err = s.Expire(); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Message(n.ID)
	if err != nil || got.State != "expired" {
		t.Fatalf("expired: %s %v", got.State, err)
	}
	if err = s.db.QueryRow(`SELECT count(*) FROM delivery_retries WHERE message_id=?`, n.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired schedule: %d %v", count, err)
	}
}

func TestRetryDispatchAndMayHaveArrived(t *testing.T) {
	s, c := newTestStore(t)
	a, b := twoSessions(t, s)
	node := mustNode(t, s, b)
	conns := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err == nil {
			conns <- ws
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	ws := <-conns
	defer ws.CloseNow()
	g := &Gateway{store: s, conns: map[string]*nodeConn{node: {id: node, ws: ws}}}
	m, _, err := send(s, a, b, "scheduled", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ApplyDelivery(m.ID, "failed", "no such local session"); err != nil {
		t.Fatal(err)
	}
	m, _, _ = s.Message(m.ID)
	g.sendDeliver(ctx, node, m)
	got, _, _ := s.Message(m.ID)
	if got.State != "failed" {
		t.Fatalf("sent early: %s", got.State)
	}
	c.t = c.t.Add(30 * time.Second)
	g.sendDeliver(ctx, node, m)
	if _, _, err = client.Read(ctx); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Message(m.ID)
	if got.State != "dispatched" {
		t.Fatalf("not dispatched: %s", got.State)
	}
	if err = s.ApplyDelivery(m.ID, "failed", "write failed; the message may have arrived"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = s.Message(m.ID)
	g.sendDeliver(ctx, node, got)
	got, _, _ = s.Message(m.ID)
	if got.State != "failed" {
		t.Fatalf("uncertain retry sent: %s", got.State)
	}
}
