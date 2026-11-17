package tui

import (
	"strings"

	"github.com/niparis/agent-net/internal/tui/tabs"
)

// The feature map reads the same tables the screens do: tabs.All for the tab
// bar, helpSections for each tab's key sections, keyBindings through
// helpEntries for the key rows, and panelTable for the fixed titles. It is
// generated into docs/feature-map.md by internal/featuremap.

// FeatureTab is one tab as the feature map lists it.
type FeatureTab struct {
	Name     string
	Key      string
	Action   string
	Panels   []FeaturePanel
	Sections []FeatureSection
}

// FeaturePanel is one fixed title that a tab's screen draws.
type FeaturePanel struct {
	Title string
	View  string // the sub-view the title sits in; empty on the tab's own screen
	Note  string
}

// FeatureSection is a sub-view or an overlay with its key rows.
type FeatureSection struct {
	Title string
	Keys  []FeatureKey
}

// FeatureKey is one key row: the keys and what they do.
type FeatureKey struct {
	Keys string
	Help string
}

// featureModel is a zero Model. The titles it gives are the ones a session
// without a CTO name shows.
var featureModel Model

// FeatureTabs returns every tab in tab order, with its panels and key sections.
func FeatureTabs() []FeatureTab {
	var out []FeatureTab
	for i, tab := range tabs.All {
		t := FeatureTab{Name: tab.Name, Key: tab.Key, Action: tab.Action}
		for _, p := range panelTable {
			if p.tab != i {
				continue
			}
			t.Panels = append(t.Panels, FeaturePanel{Title: p.title, View: featureViewTitle(p.view), Note: p.note})
		}
		section := helpTabSection(i)
		skipGlobal := !featureModel.overlayContext(section.contexts[0])
		for _, context := range section.contexts {
			t.Sections = append(t.Sections, FeatureSection{Title: featureModel.helpSubsectionTitle(context), Keys: featureKeys(context, skipGlobal)})
		}
		out = append(out, t)
	}
	return out
}

// FeatureGlobalKeys returns the fixed Global keys list.
func FeatureGlobalKeys() []FeatureKey {
	var out []FeatureKey
	for _, row := range globalHelpRows {
		out = append(out, FeatureKey{Keys: row.keys, Help: strings.ReplaceAll(row.help, "CTO", featureModel.ctoTitle())})
	}
	return out
}

// FeatureOverlays returns the key sections of the overlays and forms, in the
// order the help overlay lists them.
func FeatureOverlays() []FeatureSection {
	var out []FeatureSection
	for _, context := range featureModel.overlayContexts() {
		out = append(out, FeatureSection{Title: featureModel.helpSubsectionTitle(context), Keys: featureKeys(context, false)})
	}
	return out
}

func featureKeys(context keyContext, skipGlobal bool) []FeatureKey {
	var out []FeatureKey
	for _, entry := range featureModel.helpEntries(context, skipGlobal) {
		out = append(out, FeatureKey{Keys: strings.Join(entry.keys, ", "), Help: entry.help})
	}
	return out
}

func featureViewTitle(context keyContext) string {
	if context == "" {
		return ""
	}
	return featureModel.helpSubsectionTitle(context)
}
