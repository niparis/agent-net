package protocol

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"unicode/utf8"
)

// CharterAttributeGuide explains one attribute for owners writing a charter.
// Charters are read by agents on every turn, so each value should stay short.
type CharterAttributeGuide struct {
	Name    string
	Meaning string
	Reader  string
	Size    string
	Good    string
	Bad     string
}

var charterGuide = []CharterAttributeGuide{
	{Name: "purpose", Meaning: "What the project is.", Reader: "Agents deciding whether the project fits a request.", Size: "one sentence", Good: "Generic document and evidence API for any client.", Bad: "A long inventory of endpoints, deployment hosts and access rules."},
	{Name: "maturity", Meaning: "The project's level, followed by a few words about why.", Reader: "Agents choosing how much care, testing and review to use.", Size: "one line", Good: "growing - core API is stable; integrations are still changing.", Bad: "Critical because production uses 192.168.1.4 and token abcdef0123456789."},
	{Name: "accepts", Meaning: "What work this project's agent takes on when other agents ask.", Reader: "Other agents deciding whether and how to request work.", Size: "1-3 short items", Good: "Bug fixes in retrieval; new generic endpoints; questions about the API", Bad: "Bearer-authenticated requests from 192.168.50.65 on /v1/search"},
	{Name: "refuses", Meaning: "What work the agent declines and where to redirect it.", Reader: "Other agents deciding what to request and where to send it.", Size: "1-3 short items", Good: "Client-specific endpoints; deploy changes (ask homelab)", Bad: "Reject 192.168.1.9; token=ghp_abcdefghijklmnopqrstuvwxyz123456"},
	{Name: "feature_requests", Meaning: "How an agent should raise an idea for new work.", Reader: "Agents with an idea that is not already in scope.", Size: "one line", Good: "Propose generic additions with a reason; send client-specific ideas to the client project.", Bad: "List every endpoint proposal, requester, credential and rollout step."},
	{Name: "consumers", Meaning: "The projects that depend on this one.", Reader: "Agents judging downstream effects of a change.", Size: "project names only", Good: "tenebrae, wbg-housing", Bad: "10.0.0.12 (uid 501), /home/agent/token"},
	{Name: "escalate", Meaning: "What the agent must bring to the owner or its architect.", Reader: "The project agent deciding when and how to ask for a decision.", Size: "1-2 lines", Good: "Breaking API changes: bring options and a recommendation to the owner.", Bad: "Owner is nicolas on 192.168.1.7; call this token before asking."},
	{Name: "needs", Meaning: "Projects, MCP servers and services the agent depends on.", Reader: "Agents setting up or working in this project.", Size: "names only", Good: "quota-axi, tenebrae-library, PostgreSQL", Bad: "192.168.1.7, uid 501, /home/agent/.config/token"},
	{Name: "notes", Meaning: "Other short rules every agent working here must know.", Reader: "Agents working in this project.", Size: "1-2 lines; detail belongs in AGENTS.md", Good: "Deploys go through homelab; never deploy by hand.", Bad: "Full deploy runbook with IPs, user IDs, token values and file paths."},
}

var (
	ipv4Pattern        = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Pattern        = regexp.MustCompile(`(?i)(?:[0-9a-f]{0,4}:){2,7}[0-9a-f]{0,4}(?:%[a-z0-9_.-]+)?`)
	tokenPattern       = regexp.MustCompile(`(?i)\b(?:bearer\s+|(?:token|api[_-]?key|access[_-]?token|secret|password)\s*[:=]\s*)[a-z0-9._~+/=-]{8,}`)
	knownTokenPattern  = regexp.MustCompile(`(?i)\b(?:sk|gh[pousr]|github_pat|glpat|xox[baprs]|npm|pypi|akia)[_-][a-z0-9_-]{12,}\b`)
	jwtPattern         = regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\b`)
	windowsPathPattern = regexp.MustCompile(`(?i)(?:^|[\s="'(])(?:[a-z]:\\|\\\\)[^\s,;]+`)
)

// CharterGuide returns the guidance in attribute order. The returned slice is
// a copy so callers cannot change the shared guide.
func CharterGuide() []CharterAttributeGuide {
	return append([]CharterAttributeGuide(nil), charterGuide...)
}

// CharterGuideFor returns the guidance for one named attribute.
func CharterGuideFor(name string) (CharterAttributeGuide, bool) {
	for _, guide := range charterGuide {
		if guide.Name == name {
			return guide, true
		}
	}
	return CharterAttributeGuide{}, false
}

// CharterGuideText describes every field, including its reader, size and
// examples. It is used by the CTO tool schema so that text stays in sync with
// the shared protocol guide.
func CharterGuideText() string {
	var b strings.Builder
	for i, guide := range charterGuide {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s: %s Read by %s Size: %s. Good: %q Bad: %q.", guide.Name, guide.Meaning, guide.Reader, guide.Size, guide.Good, guide.Bad)
	}
	return b.String()
}

// CharterValueWarning advises about a value that is too detailed for a
// per-turn charter. It never rejects or changes the value.
func CharterValueWarning(attr, value string) string {
	guide, ok := CharterGuideFor(attr)
	if !ok || value == "" {
		return ""
	}
	if !containsIPAddress(value) && !containsTokenLikeString(value) && !containsAbsolutePath(value) {
		return ""
	}
	return fmt.Sprintf("too long/too detailed for a charter: keep it to %s; put detail in AGENTS.md", guide.Size)
}

// CharterWarnings returns advisory warnings for the saved charter.
func CharterWarnings(c Charter) []string {
	fields := []struct{ name, value string }{
		{"purpose", c.Purpose}, {"maturity", c.Maturity}, {"accepts", c.Accepts}, {"refuses", c.Refuses}, {"feature_requests", c.FeatureRequests}, {"consumers", c.Consumers}, {"escalate", c.Escalate}, {"needs", c.Needs}, {"notes", c.Notes},
	}
	var warnings []string
	total := 0
	for _, field := range fields {
		total += utf8.RuneCountInString(field.value)
		if warning := CharterValueWarning(field.name, field.value); warning != "" {
			warnings = append(warnings, field.name+": "+warning)
		}
	}
	if total > 1200 {
		warnings = append(warnings, "the whole charter exceeds 1200 characters: the project's agent reads it every turn")
	}
	if utf8.RuneCountInString(c.Purpose) > 160 {
		warnings = append(warnings, "purpose exceeds 160 characters: the owner reads it on the Map card")
	}
	for _, field := range []struct{ name, value string }{{"accepts", c.Accepts}, {"refuses", c.Refuses}, {"escalate", c.Escalate}} {
		items := strings.FieldsFunc(field.value, func(r rune) bool { return r == ';' || r == '\n' })
		var nonempty []string
		for _, item := range items {
			if item = strings.TrimSpace(item); item != "" {
				nonempty = append(nonempty, item)
			}
		}
		if len(nonempty) > 6 {
			warnings = append(warnings, field.name+" has more than 6 items: use short categories so the agent can scan them")
		}
		if (field.name == "accepts" || field.name == "refuses") && len(nonempty) > 0 && utf8.RuneCountInString(nonempty[0]) > 160 {
			warnings = append(warnings, "neighbors see only the first 160 characters of "+field.name+": put the main item first")
		}
	}
	return warnings
}

func containsIPAddress(value string) bool {
	for _, match := range ipv4Pattern.FindAllString(value, -1) {
		if is := parseIP(match); is {
			return true
		}
	}
	for _, match := range ipv6Pattern.FindAllString(value, -1) {
		if is := parseIP(match); is {
			return true
		}
	}
	return false
}

func parseIP(value string) bool {
	// A trailing colon is common sentence punctuation, but not part of the IP.
	return net.ParseIP(strings.TrimRight(value, ":")) != nil
}

func containsTokenLikeString(value string) bool {
	return tokenPattern.MatchString(value) || knownTokenPattern.MatchString(value) || jwtPattern.MatchString(value)
}

func containsAbsolutePath(value string) bool {
	if windowsPathPattern.MatchString(value) {
		return true
	}
	for _, field := range strings.Fields(value) {
		for _, part := range strings.Split(field, "=") {
			path := strings.Trim(part, `"'(){}[],;`)
			path = strings.TrimRight(path, ".:!?`>")
			if strings.HasPrefix(path, "/") {
				return true
			}
		}
	}
	return false
}
