package authority

import (
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

// Text is the "Delegated authority" block for a session's charter text, built
// from the scopes its role node holds and the table's phrases. Empty when the
// node holds no scope the table knows.
func Text(scopes []protocol.Scope) string {
	phrase := map[string]string{}
	for _, row := range Table {
		phrase[row.Kind] = row.Phrase
	}
	byOver := map[string]map[string]bool{}
	for _, s := range scopes {
		if phrase[s.Kind] == "" {
			continue
		}
		if byOver[s.Over] == nil {
			byOver[s.Over] = map[string]bool{}
		}
		byOver[s.Over][s.Kind] = true
	}
	if len(byOver) == 0 {
		return ""
	}
	overs := make([]string, 0, len(byOver))
	for o := range byOver {
		overs = append(overs, o)
	}
	sort.Strings(overs)
	var parts []string
	for _, over := range overs {
		var what []string
		for _, row := range Table {
			if byOver[over][row.Kind] {
				what = append(what, row.Phrase)
			}
		}
		parts = append(parts, "you may "+joinAnd(what)+" "+where(over))
	}
	return "Delegated authority: " + strings.Join(parts, "; ") + "."
}

func joinAnd(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func where(over string) string {
	kind, id, _ := protocol.ParseScopeOver(over)
	switch kind {
	case protocol.ScopeOverAll:
		return "everywhere in agent-net"
	case "team":
		return "in team " + id
	case "agent":
		return "for agent " + id
	}
	return "in " + id
}
