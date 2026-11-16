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

func TestSkillArchivePromoteNoticeOffersUndo(t *testing.T) {
	text, _, options := skillArchiveNotice("skill_archive_promote", protocol.SkillArchiveAddResult{Name: "graphify", Version: 2, State: "promoted", Change: "sc_1"})
	if want := "CTO chose version 2 of skill graphify as the latest"; text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
	if len(options) != 2 || options[0] != protocol.OpSkillArchiveUndo {
		t.Fatalf("options = %v, want the archive undo", options)
	}
}

// A candidate that intake stored says which version stays the latest, and a
// skill with no description is named, so the Inbox shows both.
func TestSkillArchiveIntakeNoticeNamesCandidatesAndEmptyDescriptions(t *testing.T) {
	_, diff, _ := skillArchiveNotice("skill_archive_intake", protocol.SkillArchiveMultiResult{Skills: []protocol.SkillArchiveAddResult{
		{Name: "graphify", Version: 3, Latest: 2, State: "added", Description: "Graph."},
		{Name: "bare", Version: 1, Latest: 1, State: "added", NoDescription: true},
	}})
	for _, want := range []string{"Not latest: graphify v3 (latest stays v2)", "No description: bare"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff %q does not contain %q", diff, want)
		}
	}
}
