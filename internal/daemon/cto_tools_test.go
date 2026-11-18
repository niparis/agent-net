package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// A call that fails says what failed. A gateway that did not answer, or a link
// that dropped, is not an old gateway, so the error must not say to upgrade it.
func TestCTOAttributionCheckNamesFailedCall(t *testing.T) {
	cases := []struct {
		name   string
		closed bool
		ctx    func(context.Context) context.Context
		want   error
	}{
		{"no answer", false, func(ctx context.Context) context.Context { return withCallWait(ctx, 50*time.Millisecond) }, errGatewayTimeout},
		{"closed link", true, func(ctx context.Context) context.Context { return ctx }, errNotConnected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &Daemon{lk: silentGatewayLink(t, tc.closed)}
			_, err := d.ctoAttributedCall(tc.ctx(context.Background()), "msg.send", map[string]string{})
			if err == nil || strings.Contains(err.Error(), "upgrade the gateway") || !strings.Contains(err.Error(), "cto.capabilities") || !errors.Is(err, tc.want) {
				t.Fatalf("attribution check error: %v", err)
			}
		})
	}
}

// silentGatewayLink is a daemon link to a gateway that reads frames and never
// answers. With closed set, the link is already dead when the test calls it.
func silentGatewayLink(t *testing.T, closed bool) *link {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	ws, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if closed {
		ws.CloseNow()
	} else {
		t.Cleanup(func() { ws.CloseNow() })
	}
	return newLink(ws, nil)
}
