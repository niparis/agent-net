package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

func oversizedToolsFixture(w, h int) (Model, *fakeTools) {
	m, s := agentToolsFixture(w, h)
	large := toolSkill("oversize-archify", "large", toolLoc(toolNodeR, "research", "/tmp/skills/oversize-archify", "user", "", "codex"))
	large.StartTokens = 310
	large.Locations[0].Manifest = &protocol.SkillManifest{Files: []protocol.SkillFile{{Path: "SKILL.md", Size: 1 << 20}, {Path: "data.bin", Size: 10 << 20}}}
	s.items = append(s.items, large)
	m = pressKey(m, "5")
	return m, s
}

func TestToolsOversizedSkillLocked(t *testing.T) {
	m, s := oversizedToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, "/")
	m = typeText(m, "oversize")
	m = pressKey(m, "enter")
	reason := "too large to manage (11 MB, the limit is 2 MB)"
	if !strings.Contains(plain(m), "×   ") || !strings.Contains(plain(m), reason) {
		t.Fatal("missing size lock:\n" + plain(m))
	}
	m = pressKey(m, " ")
	if len(m.tl.agentPending["homelab"]) != 0 || !strings.Contains(m.activeToast(), reason) {
		t.Fatal("oversized skill must not queue a toggle")
	}
	if len(s.imports) != 0 || len(s.applied) != 0 {
		t.Fatal("locked skill changed state")
	}
}

func TestToolsImportAllSkipsOversizedSkills(t *testing.T) {
	m, s := oversizedToolsFixture(120, 34)
	m = pressKey(m, "i")
	for _, want := range []string{"1 skill skipped", "oversize-archify", "11 MB", "limit is 2 MB"} {
		if !strings.Contains(plain(m), want) {
			t.Errorf("missing %s: %s", want, plain(m))
		}
	}
	if len(s.imports) != 0 || len(s.applied) != 0 {
		t.Fatal("review applied a change")
	}
	m = pressKey(m, "enter")
	if len(s.imports) != 1 || s.imports[0].Item == "skill:oversize-archify:large" || len(s.applied) != 0 {
		t.Fatalf("imports %v applied %v", s.imports, s.applied)
	}
}

func TestToolsUnstoredToggleReviewsBeforeImport(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	m = pressKey(m, "right")
	m = pressKey(m, "down")
	m = pressKey(m, "down")
	m = pressKey(m, " ")
	if m.tl.modal == nil || !strings.Contains(plain(m), "Review 1 change") || len(s.toggles) != 1 {
		t.Fatal("space must show a gateway plan")
	}
	if len(s.imports) != 0 || len(s.plans) != 0 || len(s.applied) != 0 {
		t.Fatal("review changed files")
	}
	m = pressKey(m, "enter")
	if len(s.applied) != 1 || len(s.imports) != 0 {
		t.Fatal("gateway plan must handle import on apply")
	}
}

func TestToolsOversizedGoldens(t *testing.T) {
	for _, size := range [][2]int{{120, 34}, {80, 24}} {
		m, _ := oversizedToolsFixture(size[0], size[1])
		m = pressKey(m, "right")
		m = pressKey(m, "/")
		m = typeText(m, "oversize")
		m = pressKey(m, "enter")
		golden(t, fmt.Sprintf("tools_oversized_%dx%d", size[0], size[1]), m.testView())
		m = pressKey(m, "i")
		golden(t, fmt.Sprintf("tools_import_skip_%dx%d", size[0], size[1]), m.testView())
	}
}

func TestToolsSkillSizeManifestRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		files  []protocol.SkillFile
		copies int
		want   string
	}{
		{"at limit", []protocol.SkillFile{{Size: 2 << 20}}, 1, ""},
		{"over limit", []protocol.SkillFile{{Size: 2 << 20}, {Size: 1}}, 1, "too large"},
		{"decimal size", []protocol.SkillFile{{Size: 47 * (1 << 20) / 10}}, 1, "4.7 MB"},
		{"duplicate copies", []protocol.SkillFile{{Size: 3 * (1 << 20) / 2}}, 2, ""},
		{"no manifest", nil, 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := toolSkill("fixture", "hash")
			for i := 0; i < tc.copies; i++ {
				loc := toolLoc(toolNodeR, "research", "/tmp/fixture", "user", "", "codex")
				if tc.files != nil {
					loc.Manifest = &protocol.SkillManifest{Files: tc.files}
				}
				item.Locations = append(item.Locations, loc)
			}
			got := skillSizeReason(skillItemBytes(item))
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToolsImportAllOnlyOversizedShowsExplanation(t *testing.T) {
	m, s := oversizedToolsFixture(80, 24)
	s.items = s.items[len(s.items)-1:]
	m = pressKey(m, "5")
	m = pressKey(m, "i")
	if m.tl.modal == nil || !strings.Contains(plain(m), "1 skill skipped") {
		t.Fatal(plain(m))
	}
	m = pressKey(m, "enter")
	if len(s.imports) != 0 || len(s.plans) != 0 || len(s.applied) != 0 {
		t.Fatal("oversized-only review performed a change")
	}
	m = pressKey(m, "esc")
	if m.tl.modal != nil {
		t.Fatal("cannot close oversized-only review")
	}
}

func TestToolsOversizedRowOnlyMarkerAndCost(t *testing.T) {
	for _, width := range []int{120, 80} {
		m, s := oversizedToolsFixture(width, 34)
		s.items[len(s.items)-1].StartTokens = 310
		m = pressKey(m, "5")
		m = pressKey(m, "right")
		m = pressKey(m, "/")
		m = typeText(m, "oversize")
		m = pressKey(m, "enter")
		text := plain(m)
		if strings.Contains(text, "🔒") || !strings.Contains(text, "×   oversize-archify") {
			t.Errorf("%d: need a one-column marker: %s", width, text)
		}
		if !strings.Contains(text, "~310") {
			t.Errorf("%d: oversized skill must retain StartTokens: %s", width, text)
		}
		count := strings.Count(text, "too large to manage (11 MB, the limit is 2 MB)")
		want := 2
		if width == 80 {
			want = 1
		} // Narrow rows omit state text; Space still explains it.
		if count != want {
			t.Errorf("%d: size reason must appear in the info box and wide state column, got %d copies", width, count)
		}
		m.tl.agentFilter = ""
		regular, oversized := -1, -1
		for _, line := range strings.Split(plain(m), "\n") {
			if at := strings.Index(line, "~120"); at >= 0 && strings.Contains(line, "[x]") {
				regular = ansi.StringWidth(line[:at])
			}
			if at := strings.Index(line, "~310"); at >= 0 && strings.Contains(line, "×") {
				oversized = ansi.StringWidth(line[:at])
			}
		}
		if regular < 0 || regular != oversized {
			t.Errorf("%d: cost columns differ: regular %d, oversized %d", width, regular, oversized)
		}
	}
}

// On a real machine a big skill's scan stops listing files (TooLarge, no
// sizes), as for archify (11 MB, over 200 files). It must still be locked.
func TestToolsSkillMarkedTooLargeByTheScanIsLocked(t *testing.T) {
	m, s := agentToolsFixture(120, 34)
	large := toolSkill("big-skill", "large", toolLoc(toolNodeR, "research", "/tmp/skills/big-skill", "user", "", "codex"))
	large.Locations[0].Manifest = &protocol.SkillManifest{TooLarge: true}
	s.items = append(s.items, large)
	m = pressKey(m, "5")
	m = pressKey(m, "right")
	m = pressKey(m, "/")
	m = typeText(m, "big-skill")
	m = pressKey(m, "enter")
	if !strings.Contains(plain(m), "too large to manage (over 200 files or 2 MB)") {
		t.Fatal("missing lock for a skill the scan marked too large:\n" + plain(m))
	}
	m = pressKey(m, " ")
	if len(m.tl.agentPending["homelab"]) != 0 {
		t.Fatal("a skill marked too large queued a toggle")
	}
}
