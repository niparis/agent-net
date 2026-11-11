package phone

import (
	"net/url"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The web Desk lists a sign-in ask with one button, and the button asks the
// daemon to open the allowlisted login. The page sends no command.
func TestDeskSignInOpensFromWeb(t *testing.T) {
	f := newConsoleOperator()
	f.asks = append(f.asks, protocol.Ask{
		ID: "i_signin", Kind: protocol.AskKindSignIn, State: protocol.AskStateOpen,
		From: "agent-net", FromKind: protocol.AskFromGateway, Node: "n_secretnode1",
		Question: "Sign in to Alibaba Bailian CLI on alpha-box",
		Detail:   "the Alibaba token-plan quota needs your console login",
		Next:     protocol.AskNextOwner, CreatedAt: time.Now(),
	})
	body := page1(t, f, "/")
	mustContain(t, "sign-in desk", body, "Sign in to Alibaba Bailian CLI on alpha-box", "Open sign-in")
	mustNotContain(t, "sign-in desk", body, "bl auth login")
	code := post(t, f, "/action", url.Values{"kind": {"signin"}, "id": {"i_signin"}, "action": {"open"}})
	if code != 303 {
		t.Fatalf("POST sign-in: %d", code)
	}
	if !f.has("call:" + protocol.OpSignInOpen) {
		t.Fatalf("sign_in_open was not called: %v", f.calls)
	}
}
