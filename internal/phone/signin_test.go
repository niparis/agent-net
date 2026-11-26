package phone

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	mustContain(t, "sign-in desk", body, "Sign in to Alibaba Bailian CLI on alpha-box", "Open sign-in", "Firefox", "Safari", "owner TUI")
	mustNotContain(t, "sign-in desk", body, "bl auth login")
	code := post(t, f, "/action", url.Values{"kind": {"signin"}, "id": {"i_signin"}, "action": {"open"}})
	if code != 303 {
		t.Fatalf("POST sign-in: %d", code)
	}
	if !f.has("call:" + protocol.OpSignInOpen) {
		t.Fatalf("sign_in_open was not called: %v", f.calls)
	}
}

func TestDeskSignInWebProofScenario(t *testing.T) {
	out := os.Getenv("AGENT_NET_PROOF_DIR")
	if out == "" {
		t.Skip("set AGENT_NET_PROOF_DIR to capture the web Desk")
	}
	chrome := os.Getenv("AGENT_NET_PROOF_CHROME")
	if chrome == "" {
		t.Fatal("set AGENT_NET_PROOF_CHROME to a Chromium executable")
	}
	f := newConsoleOperator()
	f.asks = []protocol.Ask{{ID: "i_signin", Kind: protocol.AskKindSignIn, State: protocol.AskStateOpen, From: "agent-net", FromKind: protocol.AskFromGateway, Node: "n_test", Question: "Sign in to Alibaba Bailian CLI on work-box", Detail: "the Alibaba token-plan quota needs your console login", Next: protocol.AskNextOwner, CreatedAt: time.Time{}}}
	body := page1(t, f, "/")
	mustContain(t, "sign-in desk", body, "Firefox", "Safari", "owner TUI")
	page := proofPage(t, body)
	shot := filepath.Join(out, "signin-web-390x844.png")
	log := proofScreenshot(t, chrome, page, 390, 844, shot)
	meta := fmt.Sprintf("build: %s\nsize: 390x844\nstep: open\naction: open the web Desk with the sign-in ask\nexpected: the ask names the owner TUI and says Firefox, not Safari\nerrors: %s\n", os.Getenv("AGENT_NET_PROOF_BUILD"), log)
	if err := os.WriteFile(filepath.Join(out, "signin-web-390x844.txt"), []byte(meta), 0600); err != nil {
		t.Fatal(err)
	}
}
