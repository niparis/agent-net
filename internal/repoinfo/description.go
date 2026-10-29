package repoinfo

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/protocol"
)

// AgentFiles are the files a project description is read from, in order. The
// first one that gives a description wins.
var AgentFiles = []string{"AGENTS.md", "CLAUDE.md"}

// maxFileRead is how much of an instruction file is read. The description is
// near the top.
const maxFileRead = 32 << 10

// ExtractDescription returns the one-line project description of an
// instruction file (AGENTS.md or CLAUDE.md), or "" when there is none.
//
// The rule:
//  1. Find the start. It is the first heading "# Project: <name>" (the owner's
//     template) if there is one, else the first "# " heading, else the top of
//     the file. Front matter and fenced code blocks are ignored.
//  2. From there, read lines until the next heading of any level. In the
//     template form the description is the first non-empty line. Otherwise it
//     is the first plain paragraph (its lines joined with spaces).
//  3. Paragraphs that are not plain text are skipped: lists, tables, quotes,
//     HTML, rules, "@file" imports, and notes to agents such as "Notes for
//     coding agents ...".
//  4. Markdown is stripped (links, images, code, emphasis, tags).
//  5. Only the first sentence is kept, without its final full stop.
//  6. The result is cut to protocol.MaxDescriptionLen characters.
func ExtractDescription(md string) string {
	md = strings.TrimPrefix(md, "\xef\xbb\xbf")
	md = strings.ReplaceAll(strings.ReplaceAll(md, "\r\n", "\n"), "\r", "\n")
	lines := dropFrontMatter(strings.Split(md, "\n"))

	// Mark the lines inside code fences.
	inCode := make([]bool, len(lines))
	fence := ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case fence != "":
			inCode[i] = true
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
		case strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~"):
			fence = t[:3]
			inCode[i] = true
		}
	}

	// Find where to start reading.
	start, template, firstH1 := 0, false, -1
	for i, l := range lines {
		if inCode[i] {
			continue
		}
		if m := h1Re.FindStringSubmatch(l); m != nil {
			if firstH1 < 0 {
				firstH1 = i
			}
			if projectRe.MatchString(m[1]) {
				start, template = i+1, true
				break
			}
		}
	}
	if !template && firstH1 >= 0 {
		start = firstH1 + 1
	}

	var para []string
	skipping := false // inside a paragraph that is not plain text
scan:
	for i := start; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		switch {
		case inCode[i]:
			// A fence is not text; it ends a paragraph.
			if len(para) > 0 {
				break scan
			}
			skipping = true
		case t == "":
			if len(para) > 0 {
				break scan
			}
			skipping = false
		case headingRe.MatchString(t):
			break scan
		case skipping:
		case len(para) == 0 && (!plainLine(t) || agentNoteRe.MatchString(t)):
			skipping = true
		default:
			para = append(para, t)
			if template {
				break scan
			}
		}
	}
	return Describe(strings.Join(para, " "))
}

// Describe turns the text of a paragraph into a description: markdown
// stripped, first sentence only, cut to the length limit.
func Describe(text string) string {
	text = stripMarkdown(text)
	if i := strings.Index(strings.ToLower(text), "[todo"); i >= 0 {
		text = strings.TrimSpace(text[:i])
		if utf8.RuneCountInString(text) < 8 { // an unfilled template
			return ""
		}
	}
	return cutDescription(firstSentence(text))
}

var (
	h1Re        = regexp.MustCompile(`^\s{0,3}#\s+(.*?)\s*#*\s*$`)
	projectRe   = regexp.MustCompile(`(?i)^project\s*:`)
	headingRe   = regexp.MustCompile(`^#{1,6}(\s|$)`)
	agentNoteRe = regexp.MustCompile(`(?i)^(` +
		`(notes?|instructions?|guidelines?|guidance|rules?|tips|context)\s+(for|to)\b.*\b(agents?|assistants?|claude|codex|copilot|llms?|ai|models?)\b` +
		`|(this|the following)\s+(file|document|section|page)\b.*\b(agents?|assistants?|claude|codex|llms?|ai)\b` +
		`)`)
	linkImgRe  = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	linkRe     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	refLinkRe  = regexp.MustCompile(`\[([^\]]+)\]\[[^\]]*\]`)
	tagRe      = regexp.MustCompile(`<[^>\n]*>`)
	boldRe     = regexp.MustCompile(`(\*\*|__)(\S(?:.*?\S)?)(\*\*|__)`)
	italicStar = regexp.MustCompile(`\*(\S(?:[^*]*\S)?)\*`)
	italicUnd  = regexp.MustCompile(`(^|[^\w])_(\S(?:[^_]*\S)?)_($|[^\w])`)
	codeRe     = regexp.MustCompile("`+([^`]*)`+")
	spaceRe    = regexp.MustCompile(`\s+`)
)

// plainLine tells whether a line can start a plain paragraph.
func plainLine(t string) bool {
	for _, p := range []string{"- ", "* ", "+ ", ">", "|", "<", "@", "[", "![", "    "} {
		if strings.HasPrefix(t, p) {
			return false
		}
	}
	if len(t) >= 3 && strings.Trim(t, "-*_= ") == "" { // rules and setext underlines
		return false
	}
	if i := strings.IndexAny(t, ".)"); i > 0 && i <= 3 && isDigits(t[:i]) && strings.HasPrefix(t[i+1:], " ") {
		return false // numbered list
	}
	return true
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func dropFrontMatter(lines []string) []string {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return lines
	}
	for i := 1; i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); t == "---" || t == "..." {
			return lines[i+1:]
		}
	}
	return lines
}

func stripMarkdown(s string) string {
	s = linkImgRe.ReplaceAllString(s, "")
	s = linkRe.ReplaceAllString(s, "$1")
	s = refLinkRe.ReplaceAllString(s, "$1")
	s = codeRe.ReplaceAllString(s, "$1")
	s = tagRe.ReplaceAllString(s, "")
	s = boldRe.ReplaceAllString(s, "$2")
	s = italicStar.ReplaceAllString(s, "$1")
	s = italicUnd.ReplaceAllString(s, "$1$2$3")
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

var abbreviations = map[string]bool{"e.g": true, "i.e": true, "vs": true, "etc": true, "approx": true, "incl": true}

// firstSentence cuts at the first ". ", "! " or "? " (or the end), except
// after a common abbreviation. A final full stop is dropped.
func firstSentence(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '.' && c != '!' && c != '?' {
			continue
		}
		if i+1 < len(s) && s[i+1] != ' ' {
			continue
		}
		if c == '.' {
			j := i
			for j > 0 && s[j-1] != ' ' {
				j--
			}
			if abbreviations[strings.ToLower(s[j:i])] {
				continue
			}
			return strings.TrimSpace(s[:i])
		}
		return strings.TrimSpace(s[:i+1])
	}
	return strings.TrimSpace(s)
}

// cutDescription cuts s to protocol.MaxDescriptionLen characters, at a word
// boundary when one is near, and marks the cut with an ellipsis.
func cutDescription(s string) string {
	if utf8.RuneCountInString(s) <= protocol.MaxDescriptionLen {
		return s
	}
	cut := string([]rune(s)[:protocol.MaxDescriptionLen-1])
	if i := strings.LastIndex(cut, " "); i > protocol.MaxDescriptionLen/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

// ReadDescription reads the description of the project in dir from the first
// of AgentFiles that gives one. It always reads the files; Resolver caches.
func ReadDescription(dir string) string {
	for _, name := range AgentFiles {
		if d := readFileDescription(filepath.Join(dir, name)); d != "" {
			return d
		}
	}
	return ""
}

func readFileDescription(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	b, _ := io.ReadAll(io.LimitReader(f, maxFileRead))
	return ExtractDescription(string(b))
}

// descCache keeps the description of each instruction file by path. An entry
// is valid while the file's modification time and size are the same, so a
// lookup costs one stat per file.
type descCache struct {
	mu sync.Mutex
	m  map[string]descEntry
}

type descEntry struct {
	mtime time.Time
	size  int64
	desc  string
}

func (c *descCache) get(dir string) string {
	for _, name := range AgentFiles {
		path := filepath.Join(dir, name)
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() {
			c.mu.Lock()
			delete(c.m, path)
			c.mu.Unlock()
			continue
		}
		c.mu.Lock()
		e, ok := c.m[path]
		c.mu.Unlock()
		if !ok || !e.mtime.Equal(fi.ModTime()) || e.size != fi.Size() {
			e = descEntry{fi.ModTime(), fi.Size(), readFileDescription(path)}
			c.mu.Lock()
			if c.m == nil {
				c.m = map[string]descEntry{}
			}
			c.m[path] = e
			c.mu.Unlock()
		}
		if e.desc != "" {
			return e.desc
		}
	}
	return ""
}
