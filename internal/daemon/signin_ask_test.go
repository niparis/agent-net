package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"path/filepath"
	"strings"
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
	good := &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "alibaba-bl", Windows: []protocol.QuotaWindow{{Label: "5-hour"}}}}}
	d.syncSignInAsks(context.Background(), good)
	if len(calls.closes) != 1 || calls.closes[0].Provider != "alibaba" || calls.closes[0].Reason != "signed_in" {
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
  "providers": [{"provider":"alibaba","windows":[{"id":"five_hour","label":"5-hour","kind":"session","percentRemaining":90}],"state":{"status":"fresh","stale":false}}]
}`
	path := fakeQuotaAXI(t, dir, body, filepath.Join(dir, "args"))
	d.quota.setPath(path)
	d.signIn.add("alibaba", "i_signin_1")
	d.recheckSignIn(context.Background(), "alibaba")
	if len(calls.closes) != 1 || calls.closes[0].Provider != "alibaba" || calls.closes[0].Reason != "signed_in" {
		t.Fatalf("recheck closes: %+v", calls.closes)
	}
}

// An ask closes only when the provider reads with windows and no error. A
// missing row or an empty reading leaves it open, and another error keeps it
// open and updates its detail, never its fixed reason.
func TestSignInAskStaysOpenUntilGoodReading(t *testing.T) {
	const first = "No console access token found. Run `bl auth login --console`"
	const second = "Request failed: 403 Forbidden"
	info, _ := protocol.SignInProvider("alibaba")
	calls := &signInGatewayCalls{}
	d := signInTestDaemon(calls)
	reading := func(errText string, windows ...protocol.QuotaWindow) *protocol.Quotas {
		return &protocol.Quotas{Providers: []protocol.QuotaProvider{{Provider: "alibaba-bl", Error: errText, Windows: windows}}}
	}
	sync := func(q *protocol.Quotas) { d.syncSignInAsks(context.Background(), q) }

	sync(reading(first))
	if len(calls.raises) != 1 || calls.raises[0].Reason != info.Reason {
		t.Fatalf("initial raise: %+v", calls.raises)
	}

	sync(reading(second))
	if len(calls.raises) != 2 || calls.raises[1].Reason != signInDetail(info.Reason, second) || !strings.HasPrefix(calls.raises[1].Reason, info.Reason) {
		t.Fatalf("a changed error must update the detail and keep the reason first: %+v", calls.raises)
	}
	sync(reading(second))
	if len(calls.raises) != 2 {
		t.Fatalf("the same error raised again: %+v", calls.raises)
	}

	sync(&protocol.Quotas{})
	sync(reading(""))
	if len(calls.closes) != 0 {
		t.Fatalf("closed without a reading that has windows: %+v", calls.closes)
	}

	sync(reading("", protocol.QuotaWindow{Label: "5-hour"}))
	if len(calls.closes) != 1 || calls.closes[0].Provider != "alibaba" || calls.closes[0].Reason != "signed_in" {
		t.Fatalf("a good reading did not close with signed_in: %+v", calls.closes)
	}
}
