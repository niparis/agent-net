package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"path/filepath"
	"sync"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

type signInGatewayCalls struct {
	mu     sync.Mutex
	raises []protocol.SignInRaise
	closes []protocol.SignInClose
}

func (c *signInGatewayCalls) call(_ context.Context, typ string, params any) (json.RawMessage, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch typ {
	case "sign_in.raise":
		c.raises = append(c.raises, params.(protocol.SignInRaise))
		return json.RawMessage(`{"ask":{"id":"i_signin_1"}}`), true, nil
	case "sign_in.close":
		c.closes = append(c.closes, params.(protocol.SignInClose))
		return json.RawMessage(`{"ok":true}`), true, nil
	}
	return nil, false, nil
}

func signInTestDaemon(calls *signInGatewayCalls) *Daemon {
	return &Daemon{
		cfg: Config{NodeID: "n_research", testGatewayCall: calls.call},
		log: log.New(io.Discard, "", 0),
	}
}

// A quota error whose remedy is a known login raises exactly one ask. The ask
// closes with "signed in" when the error clears.
func TestQuotaRaisesOneSignInAskPerProvider(t *testing.T) {
	calls := &signInGatewayCalls{}
	d := signInTestDaemon(calls)
	broken := &protocol.Quotas{Providers: []protocol.QuotaProvider{{
		Provider: "alibaba-bl", Error: "No console access token found. Run `bl auth login --console`",
	}}}
	d.syncSignInAsks(context.Background(), broken)
	d.syncSignInAsks(context.Background(), broken)
	if len(calls.raises) != 1 || calls.raises[0].Provider != "alibaba" || calls.raises[0].Machine != "" || len(calls.raises[0].Command) != 0 {
		t.Fatalf("raises: %+v", calls.raises)
	}
	if len(calls.closes) != 0 {
		t.Fatalf("closed while the error remained: %+v", calls.closes)
	}
	good := &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "alibaba-bl"}}}
	d.syncSignInAsks(context.Background(), good)
	if len(calls.closes) != 1 || calls.closes[0].Provider != "alibaba" || calls.closes[0].Reason != protocol.AskReasonSignedIn {
		t.Fatalf("close after a good reading: %+v", calls.closes)
	}
	// A dismissed ask is not raised again until the error clears.
	d.syncSignInAsks(context.Background(), broken)
	if len(calls.raises) != 2 {
		t.Fatalf("a cleared error did not allow a new ask: %+v", calls.raises)
	}
}

// The recheck after the login pane closes runs quota-axi once and closes the
// ask with "signed in" when the provider no longer reports the login remedy.
func TestSignInRecheckClosesAskAfterGoodReading(t *testing.T) {
	calls := &signInGatewayCalls{}
	d := signInTestDaemon(calls)
	dir := t.TempDir()
	body := `{
  "generatedAt": "2026-11-11T12:00:00.000Z",
  "schemaVersion": 5,
  "providers": [{"provider":"alibaba","windows":[],"state":{"status":"fresh","stale":false}}]
}`
	path := fakeQuotaAXI(t, dir, body, filepath.Join(dir, "args"))
	d.quota.setPath(path)
	d.signIn.add("alibaba", "i_signin_1")
	d.recheckSignIn(context.Background(), "alibaba")
	if len(calls.closes) != 1 || calls.closes[0].Provider != "alibaba" || calls.closes[0].Reason != protocol.AskReasonSignedIn {
		t.Fatalf("recheck closes: %+v", calls.closes)
	}
}
