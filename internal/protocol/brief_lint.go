package protocol

import (
	"fmt"
	"regexp"
	"strings"
)

// The brief lint checks the text of a worker start for six things a caller
// usually gets wrong. It tells the caller before the worker runs. A flag
// never refuses or holds a start: the owner asked for flags, not gates. The
// checks are deterministic, with no model call. A fact the lint cannot know
// (a commit the machine did not resolve, a sandbox it did not name) gives no
// flag.

const (
	BriefCheckReference     = "reference"
	BriefCheckPath          = "path"
	BriefCheckCommit        = "commit"
	BriefCheckContradiction = "contradiction"
	BriefCheckSandbox       = "sandbox"
	BriefCheckStacked       = "stacked"
)

// MaxBriefCommitIDs caps the commit IDs one start asks its machine to resolve.
const MaxBriefCommitIDs = 8

// BriefFlag is one advisory note on a brief: the check, the brief line it is
// about and why it may go wrong.
type BriefFlag struct {
	Check  string `json:"check"`
	Line   string `json:"line"`
	Reason string `json:"reason"`
}

// BriefLintFacts is what the lint learns from the machine the worker runs on.
type BriefLintFacts struct {
	// Commits maps a commit ID the brief names to whether it resolves in the
	// project repository. A missing key is unknown.
	Commits map[string]bool
	// Sandbox is the OS sandbox the worker runs in, for example "bubblewrap",
	// or "" when it runs without one or the machine did not say.
	Sandbox string
}

var (
	lintCommitIDExact = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	// A pointer to another brief or message: the worker sees only this brief.
	lintRefBriefRE = regexp.MustCompile(`(?i)\b(?:as in|as per|per|see)\s+(?:the|that|my|your|our)\s+(?:(?:previous|earlier|last|old|prior|original)\s+)?(?:brief|task|message|thread)\b|\bas (?:we )?discussed\b|\bas agreed\b`)
	// A pointer to text below or above that the brief may not have.
	lintRefBelowRE = regexp.MustCompile(`(?i)\b(?:the|this|these|those|following|listed|shown|see)\b[^.\n]{0,40}\bbelow\b`)
	lintRefAboveRE = regexp.MustCompile(`(?i)\b(?:the|this|these|those|as|see|listed|shown)\b[^.\n]{0,40}\babove\b`)
	// A token that starts like a path outside the project: home, a relative
	// climb, or a user folder. System paths such as /usr/bin are not checked.
	lintPathRE = regexp.MustCompile("(?:^|[\\s`\"'(\\[<])((?:~|\\$HOME|/|\\.\\./)[^\\s`\"'()\\[\\]<>,;]*)")
	// A commit ID is a hex word of 7 to 40 characters on a line that names a
	// commit, a sha, a hash, a revision or a head.
	lintCommitWordRE = regexp.MustCompile(`(?i)\b(?:commit|sha|hash|revision|rev|head)\b`)
	lintCommitIDRE   = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	// Done checks that need a browser, a screenshot or Docker.
	lintSandboxRE = regexp.MustCompile(`(?i)\b(?:screenshots?|screen captures?|browser|chromium|chrome|firefox|webkit|safari|playwright|selenium|headless|docker|podman)\b`)
	// Clauses end at a full stop followed by a space, or at a semicolon. A dot
	// inside a file name such as scripts/test.sh does not end one.
	lintClauseEndRE = regexp.MustCompile(`\.\s|;`)
)

// briefDirective is a pair of instructions that cannot both hold. yes is the
// instruction, no is its opposite. A clause that says no is never a yes.
type briefDirective struct {
	what string
	yes  *regexp.Regexp
	no   *regexp.Regexp
}

var briefDirectives = []briefDirective{
	{what: "the race detector (-race)",
		yes: regexp.MustCompile("(?:^|\\s|`)-race\\b"),
		no:  regexp.MustCompile(`(?i)\b(?:no|without|skip|not|never|don'?t|do not|drop)\b[^.;\n]*?-race\b`)},
	{what: "a merge to main",
		yes: regexp.MustCompile(`(?i)\bmerge\b[^.;\n]*?\b(?:to|into)\s+(?:main|master)\b`),
		no:  regexp.MustCompile(`(?i)\b(?:do not|don'?t|never|must not|not to)\s+merge\b`)},
	{what: "the full test suite",
		yes: regexp.MustCompile(`(?i)\b(?:full|whole|entire)\s+(?:test\s+)?suite\b|\bscripts/test\.sh\b`),
		no:  regexp.MustCompile(`(?i)\bchanged tests only\b|\bonly (?:the )?(?:changed|test-changed)\b|\bonly (?:the )?tests? (?:for|that cover)\b|\btest-changed\b`)},
}

// LintBrief runs the brief checks on a start. The brief is the text the worker
// reads: the structured fields rendered in their order.
func LintBrief(p WorkerStartRequest, facts BriefLintFacts) []BriefFlag {
	lines := briefLintLines(p.BriefText())
	var flags []BriefFlag
	flags = append(flags, lintReferences(lines)...)
	flags = append(flags, lintPaths(lines)...)
	flags = append(flags, lintCommits(lines, facts.Commits)...)
	flags = append(flags, lintContradictions(lines)...)
	flags = append(flags, lintSandbox(cleanBriefLines(p.Done), facts.Sandbox)...)
	flags = append(flags, lintStacked(lines)...)
	return flags
}

// BriefCommitIDs returns the commit IDs a brief names, in order, at most
// MaxBriefCommitIDs. A machine resolves them, and LintBrief reads the answer.
func BriefCommitIDs(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range briefCommitTokens(briefLintLines(text)) {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) == MaxBriefCommitIDs {
			break
		}
	}
	return out
}

// ValidBriefCommitID tells whether id has the shape of a commit ID the lint
// asks a machine to resolve: 7 to 40 lower-case hex digits.
func ValidBriefCommitID(id string) bool { return lintCommitIDExact.MatchString(id) }

// BriefCheckNames names the checks of some flags, for a log line: "none", or
// the check names in order, comma separated.
func BriefCheckNames(flags []BriefFlag) string {
	if len(flags) == 0 {
		return "none"
	}
	names := make([]string, 0, len(flags))
	for _, f := range flags {
		names = append(names, f.Check)
	}
	return strings.Join(names, ",")
}

// BriefFlagsNote is the text a caller reads for flags on a start that waits
// for a person, one line per flag.
func BriefFlagsNote(flags []BriefFlag) string {
	if len(flags) == 0 {
		return ""
	}
	lines := []string{"brief flags (advisory: they do not refuse the start):"}
	for _, f := range flags {
		lines = append(lines, fmt.Sprintf("- %s: %s Line: %q", f.Check, f.Reason, f.Line))
	}
	return strings.Join(lines, "\n")
}

func briefLintLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func briefFlag(check, line, reason string) BriefFlag {
	if r := []rune(line); len(r) > 160 {
		line = string(r[:160]) + "..."
	}
	return BriefFlag{Check: check, Line: line, Reason: reason}
}

func lintReferences(lines []string) []BriefFlag {
	var flags []BriefFlag
	for i, line := range lines {
		switch {
		case lintRefBriefRE.MatchString(line):
			flags = append(flags, briefFlag(BriefCheckReference, line,
				"points at text that is not in this brief. The worker sees only this brief, so put the text in it."))
		case i == len(lines)-1 && lintRefBelowRE.MatchString(line):
			flags = append(flags, briefFlag(BriefCheckReference, line,
				"points below, but nothing follows it in this brief."))
		case i == 0 && lintRefAboveRE.MatchString(line):
			flags = append(flags, briefFlag(BriefCheckReference, line,
				"points above, but nothing comes before it in this brief."))
		}
	}
	return flags
}

// briefOutsidePath tells whether a path token starts outside the project.
func briefOutsidePath(tok string) bool {
	for _, prefix := range []string{"~", "$HOME/", "../", "/home/", "/Users/", "/root/", "/tmp/", "/private/"} {
		if strings.HasPrefix(tok, prefix) {
			return true
		}
	}
	return false
}

func lintPaths(lines []string) []BriefFlag {
	var flags []BriefFlag
	for _, line := range lines {
		for _, m := range lintPathRE.FindAllStringSubmatch(line, -1) {
			tok := strings.TrimRight(m[1], ".:")
			if !briefOutsidePath(tok) {
				continue
			}
			flags = append(flags, briefFlag(BriefCheckPath, line,
				fmt.Sprintf("names %s, which is outside the worker's own copy of the project. Write the path from the project root, for example AGENTS.md.", tok)))
		}
	}
	return flags
}

// briefCommitTokens returns the commit-like IDs on lines that name a commit.
func briefCommitTokens(lines []string) []string {
	var out []string
	for _, line := range lines {
		if !lintCommitWordRE.MatchString(line) {
			continue
		}
		for _, id := range lintCommitIDRE.FindAllString(line, -1) {
			// A word of only digits or only the letters a to f is not a hash.
			if strings.ContainsAny(id, "0123456789") && strings.ContainsAny(id, "abcdef") {
				out = append(out, id)
			}
		}
	}
	return out
}

func lintCommits(lines []string, resolved map[string]bool) []BriefFlag {
	var flags []BriefFlag
	for _, line := range lines {
		if !lintCommitWordRE.MatchString(line) {
			continue
		}
		for _, id := range lintCommitIDRE.FindAllString(line, -1) {
			if ok, known := resolved[id]; known && !ok {
				flags = append(flags, briefFlag(BriefCheckCommit, line,
					fmt.Sprintf("names commit %s, which does not resolve in the project repository. Check the ID, or name a branch that exists.", id)))
			}
		}
	}
	return flags
}

// briefClauses splits a line into the clauses a directive can sit in.
func briefClauses(line string) []string {
	var out []string
	for _, clause := range lintClauseEndRE.Split(line, -1) {
		if clause = strings.TrimSpace(clause); clause != "" {
			out = append(out, clause)
		}
	}
	return out
}

func lintContradictions(lines []string) []BriefFlag {
	var flags []BriefFlag
	for _, d := range briefDirectives {
		var yes, no string
		for _, line := range lines {
			for _, clause := range briefClauses(line) {
				if d.no.MatchString(clause) {
					if no == "" {
						no = clause
					}
					continue
				}
				if yes == "" && d.yes.MatchString(clause) {
					yes = clause
				}
			}
		}
		if yes != "" && no != "" {
			flags = append(flags, briefFlag(BriefCheckContradiction, yes,
				fmt.Sprintf("asks for %s here, and says otherwise in %q. Keep one.", d.what, no)))
		}
	}
	return flags
}

func lintSandbox(done []string, sandbox string) []BriefFlag {
	if sandbox == "" {
		return nil
	}
	var flags []BriefFlag
	for _, line := range done {
		if lintSandboxRE.MatchString(line) {
			flags = append(flags, briefFlag(BriefCheckSandbox, line,
				fmt.Sprintf("this done check needs a browser, a screenshot or Docker, and a worker in the %s sandbox may not have one. Say how the reviewer checks it, or name a machine that can run it.", sandbox)))
		}
	}
	return flags
}

// lintStacked flags a brief that holds a second Problem, Goal or Done section:
// an old task is stacked above the current one. It flags the brief once.
func lintStacked(lines []string) []BriefFlag {
	seen := map[string]int{}
	for _, line := range lines {
		// A section heading always has its colon. A plain item such as "done"
		// is not one, even though it reads as a label.
		if !strings.Contains(line, ":") {
			continue
		}
		label, _ := briefLineLabel(line)
		switch label {
		case "problem", "goal", "done":
		default:
			continue
		}
		seen[label]++
		if seen[label] == 2 {
			return []BriefFlag{briefFlag(BriefCheckStacked, line,
				fmt.Sprintf("the brief has a second %q section, so an old task may be stacked above the current one. Send one task per start.", label))}
		}
	}
	return nil
}
