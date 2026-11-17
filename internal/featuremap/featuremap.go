// Package featuremap writes docs/feature-map.md: the tabs, panels, keys and
// agent and CTO tools of agent-net, read from the code tables. The owner and
// the agents use these names, so the page is generated and a test keeps it in
// step with the code.
package featuremap

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/toolcatalog"
	"github.com/niparis/agent-net/internal/tui"
	"github.com/niparis/agent-net/internal/tui/tabs"
)

// Path is the generated file, relative to this package folder, where go
// generate and the test run.
const Path = "../../docs/feature-map.md"

// Render returns the contents of docs/feature-map.md.
func Render() (string, error) {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# agent-net feature map\n\n")
	w("This file is generated from the code. Do not edit it by hand. Run `go generate ./internal/featuremap` to rewrite it; a test fails while it is out of date.\n\n")
	w("Use these names for screens, panels, keys and tools. The owner may say \"the Friction panel on the Quotas tab\"; this file says where that is.\n\n")

	w("## Tabs\n\n")
	w("| key | tab | action |\n|---|---|---|\n")
	tabList := tui.FeatureTabs()
	for _, tab := range tabList {
		w("| `%s` | %s | `%s` |\n", tab.Key, tab.Name, tab.Action)
	}
	w("\nThe key `%s` opens the CTO session from any tab. The CTO is not a tab.\n\n", tabs.CTOKey)

	for i, tab := range tabList {
		w("## %d. %s\n\n", i+1, tab.Name)
		w("Key `%s` opens it.\n\n", tab.Key)
		if len(tab.Panels) > 0 {
			w("### Panels\n\n")
			for _, p := range tab.Panels {
				w("- `%s`", p.Title)
				if p.View != "" {
					w(", in %s", p.View)
				}
				if p.Note != "" {
					w(" (%s)", p.Note)
				}
				w("\n")
			}
			w("\n")
		}
		w("### Keys\n\n")
		for _, section := range tab.Sections {
			w("#### %s\n\n", section.Title)
			writeKeys(w, section.Keys)
		}
	}

	w("## Global keys\n\n")
	writeKeys(w, tui.FeatureGlobalKeys())

	w("## Overlays and forms\n\n")
	w("These open over any tab. Each has its own keys.\n\n")
	for _, section := range tui.FeatureOverlays() {
		w("### %s\n\n", section.Title)
		writeKeys(w, section.Keys)
	}

	tools, err := toolRows()
	if err != nil {
		return "", err
	}
	w("## Agent and CTO tools\n\n")
	w("Tools are listed by the roles that may call them. Actions are the values of the `action` field in the tool's input schema.\n\n")
	w("| tool | roles | what it does | actions |\n|---|---|---|---|\n")
	for _, t := range tools {
		w("| `%s` | %s | %s | %s |\n", t.Name, t.Roles, cell(t.Description), t.Actions)
	}
	return b.String(), nil
}

// Generate rewrites docs/feature-map.md from the code.
func Generate() error {
	data, err := Render()
	if err != nil {
		return err
	}
	return os.WriteFile(Path, []byte(data), 0o644)
}

func writeKeys(w func(string, ...any), keys []tui.FeatureKey) {
	w("| keys | what it does |\n|---|---|\n")
	for _, k := range keys {
		w("| %s | %s |\n", cell(k.Keys), cell(k.Help))
	}
	w("\n")
}

type toolRow struct {
	Name, Roles, Description, Actions string
}

// toolRows lists the catalog tools by name, with their roles and actions.
func toolRows() ([]toolRow, error) {
	tools := append([]toolcatalog.Tool(nil), toolcatalog.Tools...)
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	var out []toolRow
	for _, tool := range tools {
		actions, err := toolActions(tool)
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", tool.Name, err)
		}
		var roles []string
		for _, role := range tool.Roles {
			roles = append(roles, string(role))
		}
		out = append(out, toolRow{
			Name:        tool.Name,
			Roles:       strings.Join(roles, ", "),
			Description: tool.Description,
			Actions:     actions,
		})
	}
	return out, nil
}

// toolActions returns the action values of the tool's input schema, and of its
// CTO schema when the CTO has its own, in order and without repeats.
func toolActions(tool toolcatalog.Tool) (string, error) {
	var seen []string
	for _, schema := range [][]byte{tool.InputSchema, tool.CTOInputSchema} {
		if len(schema) == 0 {
			continue
		}
		var parsed struct {
			Properties struct {
				Action struct {
					Enum []string `json:"enum"`
				} `json:"action"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(schema, &parsed); err != nil {
			return "", err
		}
		for _, action := range parsed.Properties.Action.Enum {
			if !slices.Contains(seen, action) {
				seen = append(seen, action)
			}
		}
	}
	if len(seen) == 0 {
		return "-", nil
	}
	quoted := make([]string, len(seen))
	for i, action := range seen {
		quoted[i] = "`" + action + "`"
	}
	return strings.Join(quoted, ", "), nil
}

// cell keeps a value inside one Markdown table cell.
func cell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}
