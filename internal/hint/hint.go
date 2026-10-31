// Package hint finds the other sessions that may help with an error (decision
// 0012). It is plain token matching: no model, no network. The daemon feeds it
// the error text of a failed tool call and the list of sessions.
package hint

import (
	"net/url"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// MaxText is how much of an error text is looked at.
const MaxText = 8 * 1024

// Candidate is what the matcher knows about one session.
type Candidate struct {
	SessionID string
	Name      string
	Hostname  string
	Online    bool
	Owner     bool // the owner pseudo-session
	Project   string
	Remote    string
	Folder    string // base name of the working folder
	Function  string
	Task      string
	Expertise string
}

// Suggestion is a candidate that matched, with its score and the reason.
type Suggestion struct {
	Candidate Candidate
	Score     int
	Why       string
}

// Self is the session the error came from.
type Self struct {
	SessionID string
	Project   string
	Hostname  string
}

var failureRE = regexp.MustCompile(`(?i)` + strings.Join([]string{
	`\bhttp[/ ]?\d*(\.\d)?\s+[45]\d\d\b`,
	`\b(status|status code|code|error|returned|response)[ :=]+"?[45]\d\d\b`,
	`\b[45]\d\d[ :-]+(bad request|unauthorized|forbidden|not found|too many requests|internal server error|bad gateway|service unavailable|gateway time-?out|timeout)`,
	`connection (refused|reset|timed out|closed)`,
	`\bconnect(ion)? (to .{1,60} )?(failed|timed out)`,
	`\b(econnrefused|econnreset|etimedout|enotfound|ehostunreach)\b`,
	`no route to host`,
	`could not resolve host|name or service not known|temporary failure in name resolution`,
	`service unavailable|bad gateway|gateway time-?out`,
	`\btimed? ?out\b`,
	`\bconnection error\b`,
	`\bcurl: \(\d+\)`,
}, "|"))

// LooksLikeFailure tells whether an output text reads like a failed network
// or service call: an HTTP 4xx/5xx status, a refused or reset connection, a
// timeout, a name that does not resolve.
func LooksLikeFailure(text string) bool {
	return failureRE.MatchString(cut(text))
}

func cut(s string) string {
	if len(s) > MaxText {
		s = strings.ToValidUTF8(s[:MaxText], "")
	}
	return s
}

// stop words: words that appear in most errors or in most descriptions, so
// they say nothing about who owns a service.
var stop = toSet(`
the and for not with from that this have has had was were are been being but you your its into onto over out off
all any can could would should will may might must just then than too very also only some such more most other
error errors failed fail failure failing warning exception traceback fatal panic
http https www com org net localhost local
status code response request requests header headers body content type length
server servers service services client clients host hostname port address url uri path file files dir folder
connection connect connected refused reset closed timeout timed time out retry
get post put delete head patch json text html data null true false none
api apis app apps application web site page pages backend frontend
log logs logging deploy deployment deployments deployed live live logs session sessions agent agents
repo repository project projects code test tests build run running runs command commands tool tools
use used using user users need needs help can does doesn did done make made work works working
unavailable available internal bad gateway not found unauthorized forbidden permission denied
see seen check checks checking read write error
cleanup tmp temp dist src lib docs scripts backup old new main dev prod scratch
`)

func toSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// words splits text into lower case words of letters and digits. A word may
// keep "-", "_" and "." inside; the compound is returned together with its
// parts.
func words(s string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// keywords returns the distinct useful words of a text: at least 4 characters,
// not all digits, not a stop word.
func keywords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range words(s) {
		if useful(w) {
			out[w] = true
		}
	}
	return out
}

func useful(w string) bool {
	if len([]rune(w)) < 4 || stop[w] {
		return false
	}
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// identifiers returns the names that identify a session's place: project,
// repository name, folder name and host name, as whole names and as parts
// ("cog-forge" gives "cogforge" and, as parts, "cog" and "forge" are too short
// to count; "billing-api" gives "billing-api" and "billing"). A name that is
// the login name or the home folder name is left out: it is in most paths on
// the machine, so it does not say who owns a service.
func identifiers(c Candidate, sameHost bool, skip map[string]bool) map[string]string {
	out := map[string]string{}
	add := func(kind, name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return
		}
		joined := strings.Join(words(name), "")
		if useful(joined) && !skip[joined] {
			out[joined] = kind
		}
		for _, w := range words(name) {
			if useful(w) && !skip[w] {
				if _, ok := out[w]; !ok {
					out[w] = kind
				}
			}
		}
	}
	add("project", c.Project)
	add("repository", repoName(c.Remote))
	// A session that works in the home folder itself has only the home folder
	// name to offer, which add drops. Its folder says nothing either way.
	add("folder", c.Folder)
	// The host name matters, but the machine of a laptop is not a service:
	// only its first label, and only when it is not generic.
	// On the machine of the failing session the name is in every path, so it
	// says nothing. A part that is the user's login or home name is in every
	// path too, even on a peer machine.
	if h := strings.SplitN(strings.TrimSpace(c.Hostname), ".", 2)[0]; h != "" && !sameHost {
		h = strings.ToLower(h)
		if joined := strings.Join(words(h), ""); useful(joined) && !skip[joined] {
			out[joined] = "host"
		}
		// Keep the whole label too, so "nicolas-mbp" still matches in full.
		if useful(h) && !skip[h] {
			out[h] = "host"
		}
		for _, w := range words(h) {
			if useful(w) && !skip[w] {
				if _, ok := out[w]; !ok {
					out[w] = "host"
				}
			}
		}
	}
	return out
}

// userNames returns the login name and the base name of the home folder of
// the daemon's user. They appear in most paths on that machine.
func userNames() map[string]bool {
	out := map[string]bool{}
	add := func(name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		if useful(name) {
			out[name] = true
		}
	}
	if name := os.Getenv("USER"); name != "" {
		add(name)
	} else if u, err := user.Current(); err == nil {
		add(u.Username)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Base(home))
	}
	return out
}

// maskHome blanks the user's home folder in a lower case error text, so its
// path components do not count as names. The result stays the same length.
func maskHome(lower string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return lower
	}
	home = strings.ToLower(filepath.Clean(home))
	if home == "" || home == "." || home == string(filepath.Separator) {
		return lower
	}
	var out strings.Builder
	for {
		i := strings.Index(lower, home)
		if i < 0 {
			break
		}
		end := i + len(home)
		if end < len(lower) && isNameByte(lower[end]) {
			// A longer name that only starts with the home folder.
			out.WriteString(lower[:end])
			lower = lower[end:]
			continue
		}
		out.WriteString(lower[:i])
		out.WriteString(strings.Repeat(" ", len(home)))
		lower = lower[end:]
	}
	out.WriteString(lower)
	return out.String()
}

func isNameByte(b byte) bool {
	return b == '-' || b == '_' || b == '.' ||
		b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// repoName is the last path element of a remote URL, without ".git".
func repoName(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	if u, err := url.Parse(remote); err == nil && u.Path != "" {
		remote = u.Path
	} else if i := strings.LastIndex(remote, ":"); i >= 0 { // git@host:owner/name.git
		remote = remote[i+1:]
	}
	return strings.TrimSuffix(path.Base(remote), ".git")
}

// Suggest returns the best candidates for an error text, at most
// protocol.MaxHints, best first. It never returns self, a session that is
// offline, an owner, or a session that works on the same project as self (its
// own code is its own business).
//
// A candidate matches when its project, repository, folder or host name
// appears in the error text, or when at least two distinct useful words of its
// expertise, function or task do. Expertise counts more than function, and
// function more than task.
func Suggest(errText string, self Self, cands []Candidate) []Suggestion {
	errText = cut(errText)
	if strings.TrimSpace(errText) == "" {
		return nil
	}
	lower := maskHome(strings.ToLower(errText))
	have := keywords(lower)
	skip := userNames()
	var out []Suggestion
	for _, c := range cands {
		if c.SessionID == "" || c.SessionID == self.SessionID || !c.Online || c.Owner {
			continue
		}
		if self.Project != "" && strings.EqualFold(c.Project, self.Project) {
			continue
		}
		score := 0
		var why []string
		for name, kind := range identifiers(c, strings.EqualFold(c.Hostname, self.Hostname), skip) {
			if have[name] || strings.Contains(lower, name) {
				score += 4
				why = append(why, kind+" name "+quote(name))
				break
			}
		}
		var matched []string
		seen := map[string]bool{}
		for _, f := range []struct {
			text   string
			weight int
		}{{c.Expertise, 3}, {c.Function, 2}, {c.Task, 1}} {
			for w := range keywords(f.text) {
				if have[w] && !seen[w] {
					seen[w] = true
					matched = append(matched, w)
					score += f.weight
				}
			}
		}
		if len(why) == 0 {
			if len(matched) < 2 {
				continue
			}
			sort.Strings(matched)
			why = append(why, "its description mentions "+quoteAll(matched))
		}
		out = append(out, Suggestion{Candidate: c, Score: score, Why: strings.Join(why, "; ")})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Candidate.Name < out[j].Candidate.Name
	})
	if len(out) > protocol.MaxHints {
		out = out[:protocol.MaxHints]
	}
	return out
}

func quote(s string) string { return `"` + s + `"` }

func quoteAll(ws []string) string {
	q := make([]string, len(ws))
	for i, w := range ws {
		q[i] = quote(w)
	}
	return strings.Join(q, ", ")
}

// ToProtocol turns suggestions into the answer's form.
func ToProtocol(ss []Suggestion) []protocol.HintSuggestion {
	out := make([]protocol.HintSuggestion, 0, len(ss))
	for _, s := range ss {
		why := s.Why
		if e := s.Candidate.Expertise; e != "" {
			why += "; it can answer about: " + e
		}
		out = append(out, protocol.HintSuggestion{SessionID: s.Candidate.SessionID, Name: s.Candidate.Name,
			Hostname: s.Candidate.Hostname, Why: why})
	}
	return out
}
