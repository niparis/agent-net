package gateway

import (
	"context"
	"strings"
	"testing"
)

func TestMachineLogRelayRejectsInvalidAndOffline(t *testing.T) {
	s, _ := newTestStore(t)
	id, _, err := s.CreateNode("box", "linux")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetNodeHello(id, "box", "test"); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: s, conns: map[string]*nodeConn{}}
	for _, tc := range []struct{ request, want string }{
		{`{"machine":"box","log":"../../private"}`, "log must be daemon or gateway"},
		{`{"machine":"box","log":"daemon","limit":1001}`, "limit must be"},
		{`{"machine":"missing","log":"daemon"}`, "unknown machine"},
		{`{"machine":"box","log":"gateway"}`, "offline"},
	} {
		if _, err := g.relayMachineLog(context.Background(), []byte(tc.request)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.request, err)
		}
	}
}
