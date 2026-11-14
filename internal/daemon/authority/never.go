// Package authority holds what the owner delegates to agents, and what he
// keeps (decision 0165). It has two parts, both plain data with small
// functions: the never list in this file, and the scope table in table.go.
// Nothing here ever answers "block": the answer is allow or no decision, and
// no decision leaves the harness or agent-net exactly as it was.
package authority

import (
	"regexp"
	"sort"
	"strings"
)

// OwnerAction is a CTO action that only the owner decides. An agent never gets
// it from a scope or from the chief. The owner's own words through the CTO
// still approve it (decision 0165): the list is about agents acting alone.
type OwnerAction struct {
	// Name is the cto_op method, and the action or area of the merged tool.
	Name string
	// Irreversible actions run at once and cannot be undone. The others wait
	// for an owner proposal when the CTO has no words of his.
	Irreversible bool
	// Lifecycle is the gateway's name for the action, when the gateway refuses
	// the CTO's own call (repo.archive, agent.tombstone).
	Lifecycle string
}

// ownerActions is the one list of owner-only CTO actions. The CTO's cto_op
// gate, its action classes, the gateway refusal and the CTO's instructions
// all read it.
var ownerActions = []OwnerAction{
	{Name: "node_remove"},
	{Name: "repo_archive", Lifecycle: "repo.archive"},
	{Name: "agent_tombstone", Lifecycle: "agent.tombstone"},
	{Name: "machine_remove"},
	{Name: "machine_rotate"},
	{Name: "process_stop", Irreversible: true},
	{Name: "app_revoke", Irreversible: true},
}

// OwnerOnly says whether name is an owner-only CTO action.
func OwnerOnly(name string) bool {
	for _, a := range ownerActions {
		if a.Name == name {
			return true
		}
	}
	return false
}

// Irreversible says whether name runs at once and cannot be undone.
func Irreversible(name string) bool {
	for _, a := range ownerActions {
		if a.Name == name {
			return a.Irreversible
		}
	}
	return false
}

// Proposal says whether name waits for an owner proposal when the CTO acts
// without the owner's words.
func Proposal(name string) bool {
	for _, a := range ownerActions {
		if a.Name == name {
			return !a.Irreversible
		}
	}
	return false
}

// RefusedForCTO says whether the gateway refuses lifecycle action (for example
// "repo.archive") from the CTO acting alone.
func RefusedForCTO(lifecycle string) bool {
	for _, a := range ownerActions {
		if a.Lifecycle != "" && a.Lifecycle == lifecycle {
			return true
		}
	}
	return false
}

// LifecycleActions lists the gateway lifecycle actions the CTO may not run
// alone.
func LifecycleActions() []string {
	var out []string
	for _, a := range ownerActions {
		if a.Lifecycle != "" {
			out = append(out, a.Lifecycle)
		}
	}
	return out
}

// ProposalNames lists the actions that wait for an owner proposal, in list
// order.
func ProposalNames() []string {
	var out []string
	for _, a := range ownerActions {
		if !a.Irreversible {
			out = append(out, a.Name)
		}
	}
	return out
}

// IrreversibleNames lists the actions that cannot be undone, in list order.
func IrreversibleNames() []string {
	var out []string
	for _, a := range ownerActions {
		if a.Irreversible {
			out = append(out, a.Name)
		}
	}
	return out
}

// OwnerActionNames lists every owner-only action, sorted.
func OwnerActionNames() []string {
	out := make([]string, 0, len(ownerActions))
	for _, a := range ownerActions {
		out = append(out, a.Name)
	}
	sort.Strings(out)
	return out
}

// neverCommands are the shell commands an agent never runs on a scope's
// authority: they delete data, spend money or use secrets. A match makes the
// answer "no decision" even when a scope row would otherwise allow it, so the
// harness asks as it does today. Each pattern is matched against the whole
// command text, in any case.
var neverCommands = []struct {
	why string
	re  *regexp.Regexp
}{
	{"deletes files", regexp.MustCompile(`(?i)(^|[\s;&|(])rm\s+(-[a-z]*r[a-z]*|--recursive)\b`)},
	{"overwrites or deletes history", regexp.MustCompile(`(?i)\bgit\s+(\S+\s+)*push\b.*(\s--force\b|\s--force-with-lease\b|\s-[a-z]*f[a-z]*\b|\s--delete\b|\s-[a-z]*d\b|\s:\S)`)},
	{"deletes a branch", regexp.MustCompile(`(?i)\bgit\s+(\S+\s+)*branch\s+(\S+\s+)*(-[a-z]*d[a-z]*|--delete)\b`)},
	{"deletes a tag", regexp.MustCompile(`(?i)\bgit\s+(\S+\s+)*tag\s+(\S+\s+)*(-[a-z]*d[a-z]*|--delete)\b`)},
	{"deletes database data", regexp.MustCompile(`(?i)\b(drop\s+(table|database|schema|index)|truncate\s+(table\s+)?\w|delete\s+from\b|dropdb\b|flushall\b|flushdb\b)`)},
	{"spends money", regexp.MustCompile(`(?i)(^|[\s;&|(])(stripe|terraform\s+(apply|destroy)|pulumi\s+up|aws\s+ec2\s+run-instances|gcloud\s+compute\s+instances\s+create|doctl\s+\S+\s+create|fly\s+(deploy|launch))\b|^\s*reason(\s|$)|api\.openai\.com|api\.anthropic\.com|openrouter\.ai|api\.x\.ai`)},
	{"uses a secret", regexp.MustCompile(`(?i)(^|[\s;&|(])(printenv|env)(\s|$)|\bgh\s+auth\s+token\b|\bsecurity\s+find-\w+-password\b|print-access-token|\bssh-add\s+-L\b|\bage\s+(-\S+\s+)*-d\b|\$\{?\w*(key|token|secret|password|passwd)\w*|(\.env\b|id_rsa|id_ed25519|\.pem\b|\.netrc|\.aws/credentials|\.npmrc|\.pypirc|\.age\b|secrets?\.(json|ya?ml|toml))`)},
}

// CommandNever reports whether a shell command is on the never list, and why.
func CommandNever(command string) (string, bool) {
	for _, n := range neverCommands {
		if n.re.MatchString(command) {
			return n.why, true
		}
	}
	return "", false
}

// compoundMarks are the shell characters that join or nest commands, or that
// expand into other text. A command that holds one is never matched whole:
// what it runs is not what the table saw. They count inside quotes too, so a
// doubtful command is left to the harness.
const compoundMarks = ";&|`$()<>\n\r{}\\"

// Compound says whether a command joins, nests or expands other commands.
func Compound(command string) bool {
	return strings.ContainsAny(command, compoundMarks)
}
