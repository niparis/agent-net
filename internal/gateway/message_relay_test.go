package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestMessageRelayExpiryAndUnknown(t *testing.T) {
	s, clock := newTestStore(t)
	clock.t = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	a, b := twoSessions(t, s)
	msg, _, err := send(s, a, b, "relay", "")
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	raw, _ := json.Marshal(protocol.QueryMessage{MessageID: msg.ID})
	if _, err := g.relayMessageBody(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("unexpired message: %v", err)
	}
	clock.t = clock.t.Add(25 * time.Hour)
	if _, err := g.relayMessageBody(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "unknown message") {
		t.Fatalf("expired message: %v", err)
	}
	raw, _ = json.Marshal(protocol.QueryMessage{MessageID: "m_missing"})
	if _, err := g.relayMessageBody(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "unknown message") {
		t.Fatalf("missing message: %v", err)
	}
}
