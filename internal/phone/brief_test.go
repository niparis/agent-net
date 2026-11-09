package phone

import (
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A brief on the web console is informational: it offers dismiss and no
// approve or refuse.
func TestBriefCardOffersDismissOnly(t *testing.T) {
	a := protocol.Ask{ID: "i_brief", Kind: protocol.AskKindBrief, State: protocol.AskStateOpen, Next: protocol.AskNextOwner,
		FromKind: protocol.AskFromGateway, From: "Daily brief 2026-11-09", Question: "Handled two fixes.\nOne decision waits.",
		CreatedAt: time.Now().Add(-time.Hour)}
	v := askCard(a, time.Now(), nil)
	if v.Form != "inbox" || !v.Dismiss || v.Approve || v.Refuse || v.CanText || v.CanMark {
		t.Fatalf("brief card = %+v", v)
	}
	if v.From != "Daily brief 2026-11-09" || v.Kind != "brief" {
		t.Fatalf("brief label = %q / %q", v.From, v.Kind)
	}
}
