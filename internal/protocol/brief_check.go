package protocol

import (
	"path"
	"regexp"
	"strings"
)

// The brief checks. Decision 0145 retired the six format rules: a brief is no
// longer refused for missing a Goal line, files, limits, a definition of done,
// how it is graded, or evidence with three lines. The only refusal left is
// overlap: a brief may not name a file a running worker or an open change in
// the same project is already working on, unless it says "overlap ok: reason".
// docs/brief-template.md stays as optional advice.

// WorkerStartModeCheck is the mode value that runs the overlap check and
// starts nothing.
const (
	WorkerStartModeCheck = "check"
)

var (
	briefPathRE    = regexp.MustCompile(`(?:\$[A-Za-z_][A-Za-z0-9_]*|[A-Za-z0-9_.@+~\-]*)(?:/[A-Za-z0-9_.@+~\-]+)+`)
	briefFileRE    = regexp.MustCompile(`(?i)\b(?:[A-Za-z0-9_\-]+\.(go|ts|tsx|js|jsx|mjs|cjs|md|markdown|py|rs|sql|sh|bash|json|yaml|yml|toml|html|css|scss|c|cc|cpp|h|hpp|java|kt|rb|php|swift|lock)|README(?:\.[A-Za-z0-9_\-]+)?)\b`)
	briefOverlapRE = regexp.MustCompile(`(?i)overlap\s+ok\s*:\s*([^\n]+)`)
	briefTargetID  = regexp.MustCompile(`(?i)\b(?:ch|w|s)_[0-9a-z]{6,}\b`)

	// Phrases that tell the worker to leave files alone. The files after a
	// prefix phrase run to the end of its clause; the files before a suffix
	// phrase run back to the start of its clause.
	briefAvoidRE      = regexp.MustCompile(`(?i)\b(?:(?:do\s+not|don'?t|never|must\s+not|not\s+to|please\s+don'?t)\s+(?:touch|edit|change|modify|alter|rewrite|delete|remove|rename|move)|avoid\s+(?:touching|editing|changing|modifying|touch|edit|change|modify))\b`)
	briefAvoidEndRE   = regexp.MustCompile(`(?i)\n|;|\.(?:\s|$)|\s(?:but|however|except|instead|rather)\b|[,\s]+(?:(?:and|or|then)\s+)?(?:change|changes|edit|edits|fix|fixes|add|adds|update|updates|write|writes|implement|make|refactor|rename|move|delete|remove|create|wire|modify|extend)\b`)
	briefAvoidAfterRE = regexp.MustCompile(`(?i)\b(?:(?:is|are)\s+(?:out\s+of\s+scope|off\s+limits|not\s+in\s+scope)|(?:is|are)\s+not\s+to\s+be\s+(?:touched|edited|changed|modified)|(?:must|should)\s+not\s+be\s+(?:touched|edited|changed|modified))\b`)
	briefAvoidStartRE = regexp.MustCompile(`(?i)[;,\n]|\.\s|\s(?:and|or|but|then)\s`)
)

// BriefTargetIDs returns the change, worker and session ids a brief names. A
// start that targets a change or worker is not refused for overlapping it.
func BriefTargetIDs(text string) map[string]bool {
	ids := map[string]bool{}
	for _, id := range briefTargetID.FindAllString(text, -1) {
		ids[strings.ToLower(id)] = true
	}
	return ids
}

// BriefWithoutAvoided blanks the parts of a brief that tell the worker to leave
// files alone, so the paths in them do not count as work the worker holds. A
// brief that asks to avoid a file another worker changes is not an overlap. The
// blanking stops at the end of the clause, so a file the brief asks the worker
// to change still counts, and any unclear wording stays an overlap.
func BriefWithoutAvoided(text string) string {
	b := []byte(text)
	blank := func(from, to int) {
		for i := from; i < to; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	for _, loc := range briefAvoidRE.FindAllStringIndex(text, -1) {
		end := len(text)
		if m := briefAvoidEndRE.FindStringIndex(text[loc[1]:]); m != nil {
			end = loc[1] + m[0]
		}
		blank(loc[0], end)
	}
	for _, loc := range briefAvoidAfterRE.FindAllStringIndex(text, -1) {
		start := 0
		for _, m := range briefAvoidStartRE.FindAllStringIndex(text[:loc[0]], -1) {
			start = m[1]
		}
		blank(start, loc[1])
	}
	return string(b)
}

// BriefPaths returns the repository paths a brief names, normalized and
// deduplicated in the order they appear. A URL is not a repository path.
func BriefPaths(text string) []string {
	type span struct{ start, end int }
	var pathSpans []span
	seen := map[string]bool{}
	var out []string
	add := func(raw string) {
		path := normalizeBriefPath(raw)
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}
	for _, idx := range briefPathRE.FindAllStringIndex(text, -1) {
		if briefURLContext(text, idx[0]) {
			continue
		}
		raw := text[idx[0]:idx[1]]
		if briefPathStop(raw) {
			continue
		}
		add(raw)
		pathSpans = append(pathSpans, span{idx[0], idx[1]})
	}
	for _, idx := range briefFileRE.FindAllStringIndex(text, -1) {
		inside := false
		for _, p := range pathSpans {
			if idx[0] >= p.start && idx[1] <= p.end {
				inside = true
				break
			}
		}
		if inside || briefURLContext(text, idx[0]) {
			continue
		}
		add(text[idx[0]:idx[1]])
	}
	return out
}

func briefURLContext(text string, start int) bool {
	prefix := text[:start]
	from := strings.LastIndexAny(prefix, " \t\r\n`\"'()[]<>")
	prefix = prefix[from+1:]
	return strings.Contains(prefix, "://") || strings.HasSuffix(prefix, ":/") || strings.HasPrefix(strings.ToLower(text[start:]), "www.")
}

func briefPathStop(raw string) bool {
	switch strings.ToLower(strings.Trim(raw, `.'"`+"`")) {
	case "and/or", "either/or", "n/a", "w/o", "24/7":
		return true
	}
	return false
}

func normalizeBriefPath(raw string) string {
	path := strings.TrimSpace(raw)
	path = strings.Trim(path, " \t\r\n`'\"()[]{}<>,.;:!?")
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimSuffix(path, "/")
	if len(path) < 4 || (!strings.ContainsAny(path, "/.") && !strings.EqualFold(path, "README")) {
		return ""
	}
	return strings.ToLower(path)
}

// BriefOverlapAllowed returns the reason after "overlap ok:". The reason must
// not be empty.
func BriefOverlapAllowed(task string) (string, bool) {
	m := briefOverlapRE.FindStringSubmatch(task)
	if m == nil {
		return "", false
	}
	reason := strings.TrimSpace(strings.Trim(m[1], "*_` "))
	if reason == "" {
		return "", false
	}
	return reason, true
}

// BriefTaskKey identifies retries of one task within a day: the caller, the
// project, and the title or the first 200 characters of the goal line.
func BriefTaskKey(caller, project, title, task string) string {
	base := strings.TrimSpace(title)
	if base == "" {
		base = briefGoalLine(task)
	}
	base = strings.ToLower(strings.Join(strings.Fields(base), " "))
	r := []rune(base)
	if len(r) > 200 {
		base = string(r[:200])
	}
	return caller + "\x1f" + project + "\x1f" + base
}

// briefGoalLine returns the text after a Goal label, or the first non-empty
// line when the brief has no Goal line. Markdown decoration is ignored.
func briefGoalLine(task string) string {
	var first string
	for _, line := range strings.Split(task, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		label, rest := briefLineLabel(line)
		if label == "goal" {
			return rest
		}
		if first == "" {
			first = strings.TrimSpace(line)
		}
	}
	return first
}

// briefLineLabel splits a markdown line into its label and the text after it.
// "## **Goal:** text" gives "goal" and "text".
func briefLineLabel(line string) (label, rest string) {
	s := strings.TrimSpace(line)
	s = strings.TrimLeft(s, " \t>#")
	// A numbered list marker such as "1. " or "2) ".
	if i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9' {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i < len(s) && (s[i] == '.' || s[i] == ')') {
			s = strings.TrimLeft(s[i+1:], " \t")
		}
	}
	s = strings.TrimLeft(s, "-*+_ \t")
	s = strings.TrimLeft(s, "*_ \t")
	if i := strings.IndexAny(s, ":："); i >= 0 {
		label = strings.Trim(s[:i], "*_# \t")
		rest = strings.TrimLeft(s[i+1:], "*_ \t")
	} else {
		label = strings.Trim(s, "*_# \t")
	}
	return strings.ToLower(strings.TrimSpace(label)), strings.TrimSpace(rest)
}

// BriefPathsShare reports whether two brief paths name the same file or a
// folder that holds the other.
func BriefPathsShare(a, b string) bool {
	a, b = normalizeBriefPath(a), normalizeBriefPath(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}

// WorkerBriefRepoRequest asks the selected daemon for the worker base tree.
type WorkerBriefRepoRequest struct {
	Path string `json:"path"`
	Base string `json:"base,omitempty"`
}

// WorkerBriefRepo holds tracked files at the base commit, never untracked files.
type WorkerBriefRepo struct {
	Root  string   `json:"root"`
	Files []string `json:"files"`
}

// Paths returns the base-tree files named by text, expanding named directories.
// A bare filename matches only when its basename is unique in the base tree.
// A root-relative spelling such as /internal/parser is accepted, but external
// absolute paths and environment-variable paths never become basename matches.
func (r WorkerBriefRepo) Paths(text string) []string {
	return r.match(text, true)
}

// FilePaths returns the tracked files a text names, without expanding a named
// directory to the files the base tree holds now. A running worker started
// from an older tree, so a directory it names must not pick up files another
// change added since. A bare filename still matches only when it is unique.
func (r WorkerBriefRepo) FilePaths(text string) []string {
	return r.match(text, false)
}

func (r WorkerBriefRepo) match(text string, dirs bool) []string {
	var out []string
	seen := map[string]bool{}
	root := strings.TrimRight(strings.ToLower(r.Root), "/")
	basenameFiles := map[string]string{}
	basenameCounts := map[string]int{}
	for _, file := range r.Files {
		file = strings.ToLower(file)
		base := path.Base(file)
		basenameFiles[base] = file
		basenameCounts[base]++
	}
	for _, candidate := range BriefPaths(text) {
		if strings.HasPrefix(candidate, "$") || strings.HasPrefix(candidate, "~") {
			continue
		}
		if !strings.Contains(candidate, "/") {
			if basenameCounts[candidate] != 1 {
				continue
			}
			candidate = basenameFiles[candidate]
		}
		if root != "" && strings.HasPrefix(candidate, root+"/") {
			candidate = strings.TrimPrefix(candidate, root+"/")
		} else {
			candidate = strings.TrimPrefix(candidate, "/")
		}
		candidate = path.Clean(candidate)
		if candidate == ".." || strings.HasPrefix(candidate, "../") {
			continue
		}
		for _, file := range r.Files {
			file = strings.ToLower(file)
			if (file == candidate || (dirs && strings.HasPrefix(file, candidate+"/"))) && !seen[file] {
				seen[file] = true
				out = append(out, file)
			}
		}
	}
	return out
}

// BriefSharedIndex reports the shared documentation and index files whose
// overlaps are useful warnings rather than reasons to refuse a worker.
func BriefSharedIndex(file string) bool {
	file = strings.ToLower(file)
	base := path.Base(file)
	return strings.HasPrefix(file, "docs/") || base == "agents.md" || base == "migrations.lock" || base == "readme" || strings.HasPrefix(base, "readme.")
}
