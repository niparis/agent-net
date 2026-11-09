package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/niparis/agent-net/internal/protocol"
)

// workersOwnerModel is a Workers panel shaped like the owner's: long profile
// names, one profile with no effort, a protected provider, and a pick in every
// column. The gateway names a pick by family and effort, and falls back to the
// raw model id for a model it has no family for (haiku).
func workersOwnerModel(width int) Model {
	m := newModel(width, 34)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{
		{Name: "claude", Models: []string{"claude-sonnet-5-5", "claude-haiku-5-5"}},
		{Name: "codex", Models: []string{"openai-codex/gpt-6.1-terra", "openai-codex/gpt-6.1-sol"}},
		{Name: "opencode", Models: []string{"opencode-go/deepseek-v4-pro"}},
	}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{
		{Name: "sonnet xhigh claude", Harness: "claude", Model: "claude-sonnet-5-5", Effort: "xhigh"},
		{Name: "terra max codex", Harness: "codex", Model: "openai-codex/gpt-6.1-terra", Effort: "max"},
		{Name: "haiku high claude", Harness: "claude", Model: "claude-haiku-5-5", Effort: "high"},
		{Name: "haiku xhigh claude", Harness: "claude", Model: "claude-haiku-5-5", Effort: "xhigh"},
		{Name: "sol medium codex", Harness: "codex", Model: "openai-codex/gpt-6.1-sol", Effort: "medium"},
		{Name: "deepseek opencode", Harness: "opencode", Model: "opencode-go/deepseek-v4-pro"},
	}
	m.snap.Scores = []protocol.ModelScore{
		{Match: "claude-sonnet-5-5", Effort: "xhigh", Score: 52.1, Source: "artificialanalysis"},
		{Match: "gpt-6.1-terra", Effort: "max", Score: 55, Source: "artificialanalysis"},
		{Match: "claude-haiku-5-5", Effort: "high", Score: 29.6, Source: "artificialanalysis"},
		{Match: "claude-haiku-5-5", Effort: "xhigh", Score: 34.3, Source: "artificialanalysis"},
		{Match: "gpt-6.1-sol", Effort: "medium", Score: 40, Source: "artificialanalysis"},
		{Match: "deepseek-v4-pro", Effort: "max", Score: 32.5, Source: "artificialanalysis"},
	}
	m.snap.WorkerProviders = []string{"claude", "codex", "opencode"}
	m.snap.ProtectedProviders.Providers = []string{"anthropic", "openai"}
	m.snap.Nodes[0].QualityPickModels = map[string]string{
		"trivial": "claude-haiku-5-5 high", "simple": "claude-haiku-5-5 high", "normal": "claude-haiku-5-5 xhigh",
		"hard": "sonnet xhigh", "very_complex": "terra max",
	}
	return m
}

// columnOf is the display column of the first occurrence of sub in line.
func columnOf(line, sub string) int {
	i := strings.Index(line, sub)
	if i < 0 {
		return -1
	}
	return ansi.StringWidth(line[:i])
}

// Every pick mark sits exactly under its T S N H V letter, at every width and
// for every row, protected or not.
func TestWorkersPanelMarksSitUnderTheirLetters(t *testing.T) {
	for _, width := range []int{80, 100, 120, 160} {
		m := workersOwnerModel(width)
		view := m.quotaModelsView(width)
		var header string
		for _, line := range view {
			plain := ansi.Strip(line)
			if strings.Contains(plain, "MODEL") && strings.Contains(plain, "HARNESS") {
				header = plain
			}
		}
		if header == "" {
			t.Fatalf("width %d: no header in\n%s", width, ansi.Strip(strings.Join(view, "\n")))
		}
		letters := map[string]int{}
		for _, l := range []string{"T", "S", "N", "H", "V"} {
			letters[l] = columnOf(header, " "+l+" ") + 1
		}
		seen := map[int]bool{}
		for _, line := range view {
			plain := ansi.Strip(line)
			if strings.Contains(plain, "= chosen") {
				continue
			}
			for i, r := range []rune(plain) {
				if r != '▸' {
					continue
				}
				col := ansi.StringWidth(string([]rune(plain)[:i]))
				ok := false
				for l, want := range letters {
					if col == want {
						ok = true
						seen[want] = true
						_ = l
					}
				}
				if !ok {
					t.Errorf("width %d: a mark is at column %d, letters are at %v:\n%s\n%s", width, col, letters, header, plain)
				}
			}
		}
		if len(seen) != 5 {
			t.Errorf("width %d: marks in %d of 5 columns", width, len(seen))
		}
	}
}

// A protected provider is marked with plain text, not an emoji, in a column to
// the right of every letter: no row holds a character a terminal may draw at
// another width.
func TestWorkersPanelProtectedMarkIsPlainTextRightOfTheMarks(t *testing.T) {
	for _, width := range []int{80, 120} {
		m := workersOwnerModel(width)
		marked := 0
		var vCol int
		for _, line := range m.quotaModelsView(width) {
			plain := ansi.Strip(line)
			if strings.Contains(plain, "MODEL") {
				vCol = columnOf(plain, " V ") + 1
			}
			if strings.Contains(plain, "protected") {
				continue // the title
			}
			if strings.Contains(plain, "🔒") {
				t.Errorf("width %d: an emoji in a table row: %q", width, plain)
			}
			if strings.Contains(plain, " prot ") {
				marked++
				if got := columnOf(plain, "prot"); got <= vCol {
					t.Errorf("width %d: prot is at column %d, not right of V at %d: %q", width, got, vCol, plain)
				}
			}
		}
		if marked != 3 {
			t.Errorf("width %d: %d protected rows are marked, want 3", width, marked)
		}
	}
}

// The MODEL column fits every model key, so no key is cut at these widths.
func TestWorkersPanelModelColumnFitsTheModelKeys(t *testing.T) {
	for _, width := range []int{80, 100, 120, 160} {
		m := workersOwnerModel(width)
		view := ansi.Strip(strings.Join(m.quotaModelsView(width), "\n"))
		for _, p := range m.snap.WorkerProfiles {
			key := pickerModelLabelForKey(protocol.PickerModelKey(p.Harness, p.Selection().LegacyModel()))
			if !strings.Contains(view, key+" ") {
				t.Errorf("width %d: the model key %q is cut or missing:\n%s", width, key, view)
			}
			if strings.Contains(view, p.Name) {
				t.Errorf("width %d: the profile name %q is in the table:\n%s", width, p.Name, view)
			}
		}
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "…") && !strings.Contains(line, "Workers ·") {
				t.Errorf("width %d: a row is cut with an ellipsis: %q", width, line)
			}
		}
	}
}

// A pick that no row stands for keeps the gateway's text.
func TestWorkersPanelPickLineKeepsUnmatchedText(t *testing.T) {
	m := workersOwnerModel(160)
	m.snap.Nodes[0].QualityPickModels["trivial"] = "gone high"
	if view := ansi.Strip(strings.Join(m.quotaModelsView(160), "\n")); !strings.Contains(view, "T gone high") {
		t.Fatalf("an unmatched pick lost its text:\n%s", view)
	}
}

// A score shows the effort it is for only when the profile runs that effort.
// deepseek has no effort and a benchmark only at max: "max" would be made up.
func TestWorkersPanelScoreEffortIsTheProfilesOwn(t *testing.T) {
	m := workersOwnerModel(120)
	view := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	for _, line := range strings.Split(view, "\n") {
		switch {
		case strings.HasPrefix(strings.TrimLeft(line, "│ "), "deepseek-v4-pro") && strings.Contains(line, "opencode"):
			if strings.Contains(line, "/max") {
				t.Errorf("the deepseek score shows a made-up effort: %q", line)
			}
		case strings.HasPrefix(strings.TrimLeft(line, "│ "), "gpt-6.1-terra:max") && strings.Contains(line, "codex"):
			if !strings.Contains(line, "55/max") {
				t.Errorf("the terra score lost its effort: %q", line)
			}
		}
	}
	// With a harness default effort that matches, it is shown.
	m.snap.Nodes[0].Harnesses[2].ModelDefaultEfforts = map[string]string{"opencode-go/deepseek-v4-pro": "max"}
	view = ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	if !strings.Contains(view, "/max") {
		t.Errorf("a default effort that matches the score is not shown:\n%s", view)
	}
}

func TestWorkersPanelOwnerGoldens(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {120, 34}, {80, 24}} {
		m := workersOwnerModel(size[0])
		m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		golden(t, fmt.Sprintf("workers_panel_owner_%dx%d", size[0], size[1]), m.testView())
	}
}

// Every row is named by its model key, with the harness in its own column.
// The free-text profile name stays out of the table.
func TestWorkersPanelRowsShowModelKeyAndHarness(t *testing.T) {
	m := workersOwnerModel(120)
	view := ansi.Strip(strings.Join(m.quotaModelsView(120), "\n"))
	for _, p := range m.snap.WorkerProfiles {
		if strings.Contains(view, p.Name) {
			t.Errorf("the profile name %q is in the table:\n%s", p.Name, view)
		}
	}
	for _, want := range []string{"claude-sonnet-5-5:xhigh", "gpt-6.1-terra:max", "gpt-6.1-sol:medium", "claude-haiku-5-5:xhigh", "deepseek-v4-pro"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing model key %q:\n%s", want, view)
		}
	}
	for _, harness := range []string{"claude", "codex", "opencode"} {
		if !strings.Contains(view, harness) {
			t.Errorf("missing harness %q:\n%s", harness, view)
		}
	}
}

// The pick line names the same keys as the rows, never the profile name.
func TestWorkersPanelPickLineUsesModelKeys(t *testing.T) {
	m := workersOwnerModel(160)
	view := ansi.Strip(strings.Join(m.quotaModelsView(160), "\n"))
	want := "T claude-haiku-5-5:high · S claude-haiku-5-5:high · N claude-haiku-5-5:xhigh · H claude-sonnet-5-5:xhigh · V gpt-6.1-terra:max"
	if !strings.Contains(view, want) {
		t.Fatalf("pick line is not %q:\n%s", want, view)
	}
}

// At 80 columns a long model key keeps its effort suffix.
func TestWorkersPanelLongKeyKeepsItsEffortAt80(t *testing.T) {
	m := newModel(80, 24)
	m.tab = 2
	m.snap.Nodes[0].Harnesses = []protocol.HarnessInfo{{Name: "codex", Models: []string{"openai-codex/gpt-6.1-terra-coder-preview"}}}
	m.snap.WorkerProfiles = []protocol.WorkerProfile{{Name: "terra coder", Harness: "codex", Model: "openai-codex/gpt-6.1-terra-coder-preview", Effort: "medium"}}
	m.snap.WorkerProviders = []string{"codex"}
	view := ansi.Strip(strings.Join(m.quotaModelsView(80), "\n"))
	row := rowContaining(view, "…:medium", "codex")
	if row == "" {
		t.Fatalf("the long key lost its effort or was not cut:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("a row at 80 columns is %d wide: %q", w, line)
		}
	}
}
