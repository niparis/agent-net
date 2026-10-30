package tui

import (
	"github.com/niparis/agent-net/internal/protocol"
	"strings"
	"testing"
)

func TestCTOUIShowAndNotice(t *testing.T) {
	m := mapModel(120, 40, fakeGraph())
	m = send(m, protocol.UIRequest{Tab: "sessions", Select: "strategies", Filter: "strategies", Why: "check work"})
	if m.tab != 0 || m.mapOn || m.selected != "s_0000000000000002" || m.filter != "strategies" || m.notice.text != "CTO: check work" {
		t.Fatalf("sessions: %+v", m)
	}
	m = send(m, protocol.UIRequest{Tab: "map", Select: "shop", Why: "inspect"})
	if !m.mapOn || m.mp.selected != "shop" || m.notice.text != "CTO: inspect" || !strings.Contains(m.View(), "CTO: inspect") {
		t.Fatalf("map: %v %q %q", m.mapOn, m.mp.selected, m.notice.text)
	}
	m = send(m, protocol.UIRequest{Tab: "quotas", Why: "quota"})
	if m.mapOn || m.tab != 2 || !strings.Contains(m.View(), "CTO: quota") {
		t.Fatalf("quotas: %v %d", m.mapOn, m.tab)
	}
	m = send(m, protocol.UIRequest{Text: "hello"})
	if m.notice.text != "CTO: hello" {
		t.Fatalf("notice: %q", m.notice.text)
	}
}
