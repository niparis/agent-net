package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/gateway"
	"github.com/niparis/agent-net/internal/protocol"
)

// An open CTO proposal is an ask on the gateway's Desk list; deciding it takes
// the ask away (decision 0090).
func TestProposalIsAnAskUntilDecided(t *testing.T) {
	g := startGateway(t, t.TempDir(), "")
	n := startNodeCfg(t, g, nodeOpts{cmdline: "/usr/bin/pi"}, nil)
	cli := cliClient(t, n)
	sid, err := g.gw.Store().UpsertSession(n.cfg.NodeID, protocol.SessionInfo{Harness: "pi", NativeID: "ask-junk", Cwd: "/tmp/judge-ask"})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.gw.Store().SetSessionOffline(n.cfg.NodeID, sid); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(protocol.SessionsArchiveRequest{IDs: []string{sid}})
	id, err := n.d.Propose(context.Background(), "sessions_archive", "", args, "Clear junk")
	if err != nil {
		t.Fatal(err)
	}
	asks := func() []protocol.Ask {
		t.Helper()
		var res protocol.AsksResult
		if err := json.Unmarshal(cli.mustCall(protocol.OpAsksList, nil), &res); err != nil {
			t.Fatal(err)
		}
		return res.Asks
	}
	list := asks()
	if len(list) != 1 || list[0].Kind != protocol.AskKindProposal || list[0].From != "CTO" || list[0].FromKind != protocol.AskFromCTO ||
		!strings.Contains(list[0].Question, "sessions archive") || !strings.Contains(list[0].Question, "Clear junk") || len(list[0].Options) != 2 || !strings.HasSuffix(list[0].Ref, ":"+id) {
		t.Fatalf("asks: %+v", list)
	}
	cli.mustCall(protocol.OpProposalAnswer, protocol.CTOProposalAnswer{ID: id, Action: "x"})
	if list := asks(); len(list) != 0 {
		t.Fatalf("decided proposal still an ask: %+v", list)
	}
}

// inboxAndLog is what the owner was told: the open Inbox rows, then the lines
// of the Desk log in the shape of the old CTO notices (decision 0090).
func inboxAndLog(s *gateway.Store) ([]protocol.InboxItem, error) {
	items, err := s.ListInbox()
	if err != nil {
		return nil, err
	}
	log, err := s.DeskLog(500)
	if err != nil {
		return nil, err
	}
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Source != "cto" {
			continue
		}
		items = append(items, protocol.InboxItem{Kind: "check", Node: "cto", Text: log[i].Text, Recommendation: log[i].Detail, Options: log[i].Options, Status: "open"})
	}
	return items, nil
}
