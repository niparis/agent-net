package tui

import (
	"bytes"
	tea "charm.land/bubbletea/v2"
	"encoding/json"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/ptyproxy"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCTOApprovedCodexFallbackRunsInApprovingTUI(t *testing.T) {
	m, _ := workerModel(100, 24)
	got, dir, _, _ := captureName(&m, "garden (test-host)")
	var q protocol.UIRequest
	if err := json.Unmarshal([]byte(`{"start_name":"garden (test-host)","start_plan":{"argv":["fake-codex","hello"],"dir":"/test/garden","note":"Herdr was not found; install it."}}`), &q); err != nil {
		t.Fatal(err)
	}
	model, cmd := m.applyCTOUI(q)
	if cmd == nil || !reflect.DeepEqual(*got, []string{"fake-codex", "hello"}) || *dir != "/test/garden" {
		t.Fatalf("fallback did not run: command=%v dir=%q", *got, *dir)
	}
	if model.(Model).enterNote != "Herdr was not found; install it." {
		t.Fatalf("missing fallback note: %q", model.(Model).enterNote)
	}
}

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

func TestCTOStartAcknowledgesAfterProxyStarts(t *testing.T) {
	s := &proposalSource{}
	m := New(s)
	var proxy *ptyproxy.Proxy
	m.execProcess = func(c tea.ExecCommand, _ tea.ExecCallback) tea.Cmd {
		proxy = c.(*ptyproxy.Proxy)
		return func() tea.Msg { return nil }
	}
	m.applyCTOUI(protocol.UIRequest{StartName: "garden", StartPlan: &protocol.EnterPlan{Argv: []string{"true"}}, StartProposalID: "p_1"})
	if proxy == nil || proxy.OnStart == nil {
		t.Fatal("missing start callback")
	}
	if len(s.startAcks) != 0 {
		t.Fatal("ack before process start")
	}
	proxy.SetStdin(strings.NewReader(""))
	var output bytes.Buffer
	proxy.SetStdout(&output)
	_ = proxy.Run()
	if proxy.Cmd.Process == nil {
		t.Fatal("process was not started")
	}
	if len(s.startAcks) != 1 || !s.startAcks[0].Started || s.startAcks[0].ID != "p_1" {
		t.Fatalf("acks: %+v", s.startAcks)
	}
}
