package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestLearnedListAndQuietHeaderCount(t *testing.T) {
	snap := fakeSnapshot()
	snap.Playbook = []protocol.PlaybookNote{
		{SubjectKind: protocol.SubjectModel, Subject: "terra", Note: "Hands back partial work.\nAsk for a checklist.",
			Source: protocol.PlaybookSourceAuto, Support: 3, TrackLine: "terra · codex: 4 runs: 1 complete, 3 partial (median 3 min) · resumes",
			UpdatedAt: t0.Add(-time.Hour).Unix()},
		{SubjectKind: protocol.SubjectProject, Subject: "shop", Note: "Tests need the TEI service.", Source: protocol.PlaybookSourceOwner},
	}
	src := &fakeSource{snap: snap}
	m := New(src)
	m.now = func() time.Time { return t0 }
	m = refresh(m, src)
	if line := m.titleLine(160); !strings.Contains(line, "learned 1 new") {
		t.Errorf("header: %q", line)
	}
	m = keys(m, "L")
	v := m.View()
	for _, want := range []string{"Learned", "terra", "auto, support 3", "Hands back partial work.", "4 runs: 1 complete, 3 partial", "shop", "Tests need the TEI service."} {
		if !strings.Contains(v, want) {
			t.Errorf("learned view lacks %q:\n%s", want, v)
		}
	}
	m = keys(m, "esc")
	if strings.Contains(m.View(), "auto, support 3") {
		t.Error("esc did not close the learned list")
	}
}
