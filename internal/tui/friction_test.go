package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/niparis/agent-net/internal/protocol"
)

// frictionSource answers the Work tab's friction read, so the golden test
// opens the tab and shows what the screen builds from a gateway answer.
type frictionSource struct {
	*fakeSource
	result protocol.FrictionFindResult
}

func (f *frictionSource) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method == protocol.OpFrictionFind {
		return json.Marshal(f.result)
	}
	return nil, nil
}

// Decision 0170: the Work tab has one read-only friction section, with the
// newest reports and a count per project. Step: open tab 3, then read the
// frames at 80x24 and 120x34.
func TestFrictionPanelGoldens(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}} {
		base := &fakeSource{snap: fakeSnapshot(), events: fakeEvents()}
		base.usage, base.counts, base.tops = fakeUsage(), fakeCounts(), fakeTops()
		src := &frictionSource{fakeSource: base, result: protocol.FrictionFindResult{
			Entries: []protocol.FrictionView{
				{ID: 1, Project: "demo", Session: "s_1", Harness: "opencode", Area: protocol.FrictionCI, Status: protocol.FrictionTriaged,
					Text: `"the suite flaked twice"`, CreatedAt: t0.Add(-12 * time.Minute).Unix()},
				{ID: 2, Project: "agent-net", Session: "s_2", Harness: "claude", Area: protocol.FrictionTooling, Status: protocol.FrictionNew,
					Text: `"the tool schema was missing"`, CreatedAt: t0.Add(-2 * time.Hour).Unix()},
			},
			Counts: []protocol.FrictionProjectCount{{Project: "demo", Count: 3}, {Project: "agent-net", Count: 1}},
			Total:  4,
		}}
		m := New(src)
		m.loc, m.now = time.UTC, func() time.Time { return t0 }
		m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = refresh(m, src)
		m = keys(m, "3")
		m = refresh(m, src)
		if !m.frictionLoaded {
			t.Fatalf("%dx%d: the Work tab did not read friction", size[0], size[1])
		}
		view := m.testView()
		plain := ansi.Strip(view)
		for _, want := range []string{"Friction", "By project: demo 3 · agent-net 1", "demo · ci · opencode · triaged · 12 min ago", `"the suite flaked twice"`, "2 more reports"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("%dx%d lacks %q:\n%s", size[0], size[1], want, plain)
			}
		}
		for _, line := range lines(view) {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("%dx%d wide line: %q", size[0], size[1], line)
			}
		}
		golden(t, fmt.Sprintf("friction_%dx%d", size[0], size[1]), view)
	}
}
