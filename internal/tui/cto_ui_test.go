package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestCTOUIShowAndNotice(t *testing.T) {
	m := mapModel(120, 40, fakeGraph())
	m = send(m, protocol.UIRequest{Tab: "sessions", Select: "strategies", Filter: "strategies", Why: "check work"})
	if m.tab != 0 || m.mapOn || m.selected != "s_0000000000000002" || m.filter != "strategies" || m.notice.text != "CTO: check work" {
		t.Fatalf("sessions: %+v", m)
	}
	m = send(m, protocol.UIRequest{Tab: "graph", Select: "shop", Why: "inspect"})
	if m.mapOn || m.tab != graphTab || m.mp.selected != "shop" || m.notice.text != "CTO: inspect" || !strings.Contains(m.testView(), "CTO: inspect") {
		t.Fatalf("map: %v %q %q", m.mapOn, m.mp.selected, m.notice.text)
	}
	m = send(m, protocol.UIRequest{Tab: "quotas", Why: "quota"})
	if m.mapOn || m.tab != 2 || !strings.Contains(m.testView(), "CTO: quota") {
		t.Fatalf("quotas: %v %d", m.mapOn, m.tab)
	}
	m = send(m, protocol.UIRequest{Text: "hello"})
	if m.notice.text != "CTO: hello" {
		t.Fatalf("notice: %q", m.notice.text)
	}
}

func TestCTONoticeVisibleInOverlayAndOneLine(t *testing.T) {
	m := mapModel(80, 24, fakeGraph())
	m.help = true
	m = send(m, protocol.UIRequest{Text: strings.Repeat("long notice ", 30)})
	if !strings.Contains(m.testView(), "CTO: long notice") {
		t.Fatal("notice missing from overlay")
	}
	if line, ok := m.noticeLine(80); !ok || strings.Contains(line, "\n") {
		t.Fatal("CTO notice wrapped")
	}
}

func TestCTOUISelectsOfflineSessionAndMapProject(t *testing.T) {
	m := mapModel(120, 40, fakeGraph())
	m = send(m, protocol.UIRequest{Tab: "sessions", Select: "old-session"})
	if m.selected != "s_0000000000000006" {
		t.Fatalf("offline selection: %s", m.selected)
	}
	m = send(m, protocol.UIRequest{Tab: "graph", Select: "web"})
	if m.mp.selected != "shop" {
		t.Fatalf("map project: %s", m.mp.selected)
	}
}

func TestCTOMapFilterClearsWhenOwnerLeavesMap(t *testing.T) {
	m := openMap(t, mapModel(120, 40, fakeGraph()))
	m = send(m, protocol.UIRequest{Tab: "graph", Filter: "shop"})
	if len(m.mapSelectable()) != 1 {
		t.Fatalf("filter not applied: %v", m.mapSelectable())
	}
	m = send(m, mapMsg{view: fakeGraph()})
	m = keys(m, "esc")
	m = openMap(t, m)
	if len(m.mapSelectable()) <= 1 {
		t.Fatalf("filter remained after leaving map: %v", m.mapSelectable())
	}
}

func TestCTOUISelectOverridesActiveFilter(t *testing.T) {
	m := mapModel(120, 40, fakeGraph())
	m.activeOnly = true
	m.snap.Agents[1].LastSeen = t0.Add(-2 * time.Hour).Unix()
	m = send(m, protocol.UIRequest{Tab: "sessions", Select: "strategies"})
	if m.selected != "s_0000000000000002" {
		t.Fatalf("selected %s while active filter set", m.selected)
	}
}

func TestHerdrOpenNoticeHasNoCTOPrefix(t *testing.T) {
	m := mapModel(80, 24, fakeGraph())
	var q protocol.UIRequest
	if err := json.Unmarshal([]byte(`{"text":"opened garden in herdr","herdr_opened":true}`), &q); err != nil {
		t.Fatal(err)
	}
	m = send(m, q)
	if m.notice.text != "opened garden in herdr" {
		t.Fatalf("notice %q", m.notice.text)
	}
}

func TestProposalResultKeepsNewHerdrOpenNotice(t *testing.T) {
	m := mapModel(80, 24, fakeGraph())
	var q protocol.UIRequest
	if err := json.Unmarshal([]byte(`{"text":"opened garden in herdr","herdr_opened":true}`), &q); err != nil {
		t.Fatal(err)
	}
	m = send(m, q)
	m = send(m, proposalAnswerMsg{node: "n_test", id: "p_1", action: "a", items: []protocol.CTOProposal{{NodeID: "n_test", ID: "p_1", Note: "p_1 approved and applied"}}})
	if m.notice.text != "opened garden in herdr" {
		t.Fatalf("notice overwritten: %q", m.notice.text)
	}
}

func TestDaemonFallbackNoticeHasNoCTOPrefix(t *testing.T) {
	m := mapModel(80, 24, fakeGraph())
	var q protocol.UIRequest
	if err := json.Unmarshal([]byte(`{"text":"Herdr was not found; install it","daemon_notice":true}`), &q); err != nil {
		t.Fatal(err)
	}
	m = send(m, q)
	if m.notice.text != "Herdr was not found; install it" {
		t.Fatalf("notice %q", m.notice.text)
	}
}
