package protocol

import "strings"

// ModelDrift is one line when a session runs another model than the one
// agent-net asked for, and empty when they agree or either one is unknown.
// The ask may be an alias or a prefix of the full name ("sonnet",
// "claude-sonnet" for "claude-sonnet-5-5"), and a provider prefix, an effort
// or a context suffix ("[1m]") does not count as a difference.
func ModelDrift(requested, running string) string {
	ask, run := driftName(requested), driftName(running)
	if ask == "" || run == "" || strings.Contains(run, ask) || strings.Contains(ask, run) {
		return ""
	}
	return "asked " + strings.TrimSpace(requested) + ", runs " + strings.TrimSpace(running)
}

func driftName(model string) string {
	fields := strings.Fields(strings.ToLower(model))
	if len(fields) == 0 {
		return ""
	}
	name := fields[0]
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.Index(name, "["); i >= 0 {
		name = name[:i]
	}
	return name
}
