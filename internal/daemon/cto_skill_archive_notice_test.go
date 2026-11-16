package daemon

import (
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/protocol"
)

func TestSkillArchiveIntakeNoticeNamesMachinesNotRead(t *testing.T) {
	text, diff, _ := skillArchiveNotice("skill_archive_intake", protocol.SkillArchiveMultiResult{
		Skills: []protocol.SkillArchiveAddResult{},
		Machines: []protocol.SkillArchiveMachine{
			{Name: "alpha", Result: "scanned"},
			{Name: "beta", Result: "offline"},
			{Name: "gamma", Result: "skipped", Reason: "no inventory report yet"},
		},
		Skipped: []protocol.SkillArchiveSkipped{{Name: "x", Reason: "invalid skill file"}},
	})
	if !strings.Contains(text, "already has every skill") {
		t.Fatalf("text: %q", text)
	}
	if want := "Not read: beta offline, gamma skipped. 1 skill not imported."; diff != want {
		t.Fatalf("diff = %q, want %q", diff, want)
	}
}
