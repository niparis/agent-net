package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// grokUsage is the usage report of one machine with one xai (grok) window.
func grokUsage(at time.Time, percent float64, resets time.Time) protocol.UsageReport {
	return protocol.UsageReport{Quotas: &protocol.Quotas{GeneratedAt: at.Unix(), Providers: []protocol.QuotaProvider{{Provider: "grok",
		Windows: []protocol.QuotaWindow{{ID: "monthly", Label: "monthly", PercentUsed: percent, ResetsAt: resets.Unix()}}}}}}
}

// The 2026-11-18 case: the lead runs xai/grok-4.6, which has no quota until
// 2026-11-21. A request to one of its members must reach the member as
// addressed, not wait with a lead that cannot run.
func TestLeadRouteLeadOutOfQuotaDeliversToMemberAsAddressed(t *testing.T) {
	r := newLeadRig(t)
	r.clk.set(time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC))
	if _, err := r.s.db.Exec(`UPDATE sessions SET harness='pi', model='xai/grok-4.6' WHERE id=?`, r.lead); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: r.s}
	g.usage.set(r.node, r.clk.now(), grokUsage(r.clk.now(), 100, time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)))
	g.refreshLeadQuota()
	m, _, note, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member2, Body: "truenas is down"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != r.member2 {
		t.Fatalf("message went to %s, want the member %s as addressed (lead %s is out of quota)", m.To, r.member2, r.lead)
	}
	if strings.Contains(note, "Redirected") || !strings.Contains(note, "Lead has no quota until 2026-11-21 00:00 UTC; delivered as addressed.") {
		t.Fatalf("receipt %q does not say the lead has no quota until the reset", note)
	}
}

// Once the lead's quota comes back, the next tick reads it and the request is
// redirected to the lead again.
func TestLeadRouteLeadQuotaBackRedirectsAgain(t *testing.T) {
	r := newLeadRig(t)
	r.clk.set(time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC))
	if _, err := r.s.db.Exec(`UPDATE sessions SET harness='pi', model='xai/grok-4.6' WHERE id=?`, r.lead); err != nil {
		t.Fatal(err)
	}
	g := &Gateway{store: r.s}
	resets := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	g.usage.set(r.node, r.clk.now(), grokUsage(r.clk.now(), 100, resets))
	g.refreshLeadQuota()
	g.usage.set(r.node, r.clk.now(), grokUsage(r.clk.now(), 20, resets))
	g.refreshLeadQuota()
	m, _, note, err := r.s.StoreLeadMessage(protocol.MsgSend{Sender: r.outsider, Recipient: r.member2, Body: "truenas is down"})
	if err != nil {
		t.Fatal(err)
	}
	if m.To != r.lead || !strings.Contains(note, "Redirected") {
		t.Fatalf("message went to %s with receipt %q, want the lead %s back on quota", m.To, note, r.lead)
	}
}
