package daemon

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/niparis/agent-net/internal/protocol"
)

type charterSuggestion struct {
	Node          string   `json:"node"`
	Field         string   `json:"field"`
	OldValue      string   `json:"old_value"`
	ProposedValue string   `json:"proposed_value"`
	MessageIDs    []string `json:"message_ids"`
}
type charterSuggestionResult struct {
	Suggestions []charterSuggestion      `json:"suggestions"`
	Evidence    []protocol.CharterSignal `json:"evidence"`
}

var ownerJob = regexp.MustCompile(`(?i)\b(?:all\s+)?(?:this\s+)?([^.!?;]{1,100}?)\s+(?:is|are)\s+(?:his|its)(?:\s+job)?\b`)
var redirectTask = regexp.MustCompile(`(?i)^(.{1,100}?)\s+(?:belongs|goes|should go|is handled)\s+(?:in|to|by|with)\b`)

func jobPhrase(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, ":\n"); i >= 0 {
		s = strings.TrimSpace(s[i+1:])
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "github") && (strings.Contains(lower, "token") || strings.Contains(lower, "secret")) {
		return "GitHub secrets"
	}
	for _, prefix := range []string{"all this ", "this ", "the "} {
		if strings.HasPrefix(strings.ToLower(s), prefix) {
			s = strings.TrimSpace(s[len(prefix):])
		}
	}
	s = strings.TrimSpace(strings.TrimSuffix(s, " things"))
	if s == "" || strings.EqualFold(s, "this") || len([]rune(s)) > 100 {
		return ""
	}
	return s
}
func signalTask(v protocol.CharterSignal) (string, string) {
	if v.Outcome == "redirect" && v.RedirectTo != "" {
		if m := redirectTask.FindStringSubmatch(strings.TrimSpace(v.Reason)); len(m) > 1 {
			return v.RedirectTo, jobPhrase(m[1])
		}
	}
	if m := ownerJob.FindStringSubmatch(v.Prompt); len(m) > 1 {
		return v.To, jobPhrase(m[1])
	}
	return "", ""
}

func charterSuggestions(g protocol.Graph, signals []protocol.CharterSignal) charterSuggestionResult {
	result := charterSuggestionResult{Suggestions: []charterSuggestion{}, Evidence: []protocol.CharterSignal{}}
	nodes := map[string]protocol.Node{}
	for _, n := range g.Nodes {
		nodes[n.Name] = n
	}
	index := map[string]int{}
	for _, v := range signals {
		if len(result.Evidence) >= 40 {
			break
		}
		// Keep excerpts and outcomes as data, bounded even if an old client stored more.
		cut := func(s string, n int) string {
			r := []rune(s)
			if len(r) > n {
				return string(r[:n])
			}
			return s
		}
		v.Prompt = cut(v.Prompt, 300)
		v.Reason = cut(v.Reason, 300)
		v.RedirectTo = cut(v.RedirectTo, 100)
		result.Evidence = append(result.Evidence, v)
		target, task := signalTask(v)
		if target == "" || task == "" || target == v.From {
			continue
		}
		targetNode, ok := nodes[target]
		if !ok {
			continue
		}
		sourceNode, sourceOK := nodes[v.From]
		add := func(node, field, old, addition string) {
			if strings.Contains(strings.ToLower(old), strings.ToLower(addition)) {
				return
			}
			key := node + "\x00" + field + "\x00" + strings.ToLower(addition)
			if i, ok := index[key]; ok {
				for _, id := range result.Suggestions[i].MessageIDs {
					if id == v.MessageID {
						return
					}
				}
				result.Suggestions[i].MessageIDs = append(result.Suggestions[i].MessageIDs, v.MessageID)
				return
			}
			proposed := addition
			if strings.TrimSpace(old) != "" {
				proposed = strings.TrimSpace(old) + "; " + addition
			}
			if len(proposed) > protocol.CharterValueMaxBytes {
				return
			}
			index[key] = len(result.Suggestions)
			result.Suggestions = append(result.Suggestions, charterSuggestion{Node: node, Field: field, OldValue: old, ProposedValue: proposed, MessageIDs: []string{v.MessageID}})
		}
		add(target, "accepts", targetNode.Charter.Accepts, task)
		if sourceOK {
			add(v.From, "needs", sourceNode.Charter.Needs, target+" for "+task)
		}
	}
	sort.SliceStable(result.Suggestions, func(i, j int) bool { return result.Suggestions[i].Node < result.Suggestions[j].Node })
	// Bound the actual JSON, including escaping, below the tool's 20 KB limit.
	for {
		b, _ := json.Marshal(result)
		if len(b) < 19000 {
			break
		}
		if len(result.Suggestions) > 0 {
			result.Suggestions = result.Suggestions[:len(result.Suggestions)-1]
		} else if len(result.Evidence) > 0 {
			result.Evidence = result.Evidence[:len(result.Evidence)-1]
		} else {
			break
		}
	}
	return result
}
