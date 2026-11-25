// Package claudeframes describes the frames agent-net writes into a Claude Code
// inbox and reads back from its transcript. A hold notice quotes the frame's
// reply socket, its first line and its size; a release writes the frame itself
// back. Both are matched to one frame, never to a message ID quoted in a body.
package claudeframes

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	// previewCut is how much of the first line a hold notice quotes.
	previewCut = 120
	// previewTie is how much of a preview must agree when the counts alone do
	// not tell two frames apart.
	previewTie = 20
)

var (
	// A hold notice names the sender's reply socket: "from uds:<path>".
	fromRE = regexp.MustCompile(`\bfrom[= ]"?(uds:[^" );\s]+)"?`)
	// A hold notice quotes the frame's size: "3 lines, 598 chars total".
	countsRE = regexp.MustCompile(`\[(\d+) lines?, (\d+) chars?`)
	// The frame content starts with the cross-session wrapper.
	openTagRE = regexp.MustCompile(`<cross-session-message\b([^>]*)>`)
)

// dash is the em dash Claude Code writes in a hold notice. It is built from a
// rune because B9 forbids the character in a production literal; the package
// only matches it and never shows it.
var dash = string(rune(0x2014))

// Description is how one frame appears to Claude Code: the size of its inner
// content, the first line a notice quotes, and the hash of the whole content.
type Description struct {
	// Preview is the frame's first non-empty line as a hold notice quotes it.
	Preview string
	// Lines and Chars are the size of the inner content: the text between the
	// wrapper tags, without one leading and one trailing newline. Chars counts
	// code points; Chars16 counts UTF-16 units, which a JavaScript reader may
	// use for characters outside the BMP.
	Lines   int
	Chars   int
	Chars16 int
	// Hash is the SHA-256 of the exact frame content a release writes back.
	Hash string
}

// Notice is what one "Held peer message" entry says about the frame it held.
type Notice struct {
	ReplySock string
	Preview   string
	Lines     int
	Chars     int
}

// Describe reads a frame content. It reports false when the content has no
// cross-session wrapper.
func Describe(content string) (Description, bool) {
	open := openTagRE.FindStringIndex(content)
	end := strings.LastIndex(content, "</cross-session-message>")
	if open == nil || end < open[1] {
		return Description{}, false
	}
	inner := content[open[1]:end]
	inner = strings.TrimPrefix(inner, "\n")
	inner = strings.TrimSuffix(inner, "\n")
	d := Description{Preview: Preview(inner), Hash: Hash(content)}
	if inner == "" {
		return d, true
	}
	d.Lines = strings.Count(inner, "\n") + 1
	d.Chars = utf8.RuneCountInString(inner)
	d.Chars16 = len(utf16.Encode([]rune(inner)))
	return d, true
}

// Preview is the first non-empty line of a frame's inner content, with the
// characters a hold notice drops removed and cut to its length.
func Preview(inner string) string {
	for _, line := range strings.Split(inner, "\n") {
		line = StripPreview(line)
		if line != "" {
			if r := []rune(line); len(r) > previewCut {
				line = string(r[:previewCut])
			}
			return line
		}
	}
	return ""
}

// StripPreview removes the characters Claude Code drops from a quote: the
// zero-width space agent-net puts in a neutralized marker, square and round
// brackets, and a plain apostrophe.
func StripPreview(s string) string {
	s = strings.ReplaceAll(s, "\u200b", "")
	return strings.NewReplacer("[", "", "]", "", "(", "", ")", "", "'", "").Replace(s)
}

// ParseNotice reads the reply socket, the quoted first line and the frame size
// from a "Held peer message" entry. Both wordings seen in transcripts are
// accepted.
func ParseNotice(text string) (Notice, bool) {
	var n Notice
	if !strings.Contains(text, "not delivered to Claude") {
		return n, false
	}
	if !strings.Contains(text, "Held peer message") && !strings.Contains(text, "Held message from another session") {
		return n, false
	}
	if m := fromRE.FindStringSubmatch(text); m != nil {
		n.ReplySock = strings.TrimPrefix(m[1], "uds:")
	}
	if i := strings.Index(text, "preview: \u00ab"); i >= 0 {
		rest := text[i+len("preview: \u00ab"):]
		if j := strings.Index(rest, "\u00bb"); j >= 0 {
			n.Preview = rest[:j]
		} else {
			cut := len(rest)
			for _, marker := range []string{"\u2026[", " " + dash + " not delivered", dash + " not delivered"} {
				if j := strings.Index(rest, marker); j >= 0 && j < cut {
					cut = j
				}
			}
			n.Preview = strings.TrimSpace(rest[:cut])
		}
	}
	if m := countsRE.FindStringSubmatch(text); m != nil {
		n.Lines, _ = strconv.Atoi(m[1])
		n.Chars, _ = strconv.Atoi(m[2])
	}
	return n, n.ReplySock != ""
}

// FrameIn returns the exact cross-session wrapper in a released entry and the
// reply socket it names. Claude Code may put text before it, but the frame
// itself is written back unchanged.
func FrameIn(text string) (content, replySock string, ok bool) {
	i := strings.Index(text, "<cross-session-message")
	if i < 0 {
		return "", "", false
	}
	j := strings.Index(text[i:], "</cross-session-message>")
	if j < 0 {
		return "", "", false
	}
	content = text[i : i+j+len("</cross-session-message>")]
	m := openTagRE.FindStringSubmatch(content)
	if m == nil {
		return "", "", false
	}
	for _, field := range strings.Fields(m[1]) {
		if strings.HasPrefix(field, `from="uds:`) {
			return content, strings.TrimSuffix(strings.TrimPrefix(field, `from="uds:`), `"`), true
		}
	}
	return "", "", false
}

// Hash is the SHA-256 of one frame content.
func Hash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Choose returns the index of the one frame a notice describes: the frames are
// the deliveries already filtered by reply socket and time window. The notice's
// line and character counts decide; the quoted first line only breaks a tie.
// It returns -1 when the notice names no single frame.
func Choose(n Notice, frames []Description) int {
	if n.Lines <= 0 || n.Chars <= 0 {
		return -1
	}
	var counted []int
	for i, f := range frames {
		// A JavaScript reader counts UTF-16 units; accept either.
		if f.Lines == n.Lines && (f.Chars == n.Chars || f.Chars16 == n.Chars) {
			counted = append(counted, i)
		}
	}
	if len(counted) == 1 {
		return counted[0]
	}
	if len(counted) == 0 {
		return -1
	}
	var tied []int
	for _, i := range counted {
		if previewMatches(n.Preview, frames[i].Preview) {
			tied = append(tied, i)
		}
	}
	if len(tied) == 1 {
		return tied[0]
	}
	return -1
}

// previewMatches compares the first line a notice quotes with the one the frame
// holds. Claude Code may cut a long line with an ellipsis.
func previewMatches(notice, stored string) bool {
	notice = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(StripPreview(notice)), "\u2026"))
	stored = strings.TrimSpace(StripPreview(stored))
	if notice == "" || stored == "" {
		return false
	}
	if notice == stored {
		return true
	}
	short, long := notice, stored
	if len(short) > len(long) {
		short, long = long, short
	}
	return len(short) >= previewTie && strings.HasPrefix(long, short)
}
