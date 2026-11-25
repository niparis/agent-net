package protocol

import "strings"

// OwnerTypedSeparator stands where a pasted block was removed from the owner's
// prompt. It is not white space, so OwnerWordsMatch cannot join the words on
// either side of a paste into one quote.
const OwnerTypedSeparator = "\x00"

const (
	pastedTagName   = "pasted_content"
	reminderTagName = "system-reminder"
)

// OwnerTypedSegments returns the parts of a prompt the owner typed, in order,
// with every pasted block and every system-reminder block removed.
//
// For each kind of block, only the text before the first opening tag and after
// the last closing tag is kept. Everything from the first opening tag to the
// last closing tag is dropped, including text between two blocks. An opening
// tag after the last closing tag drops the rest. This is deliberately blunt:
// Claude Code writes the same id on a block's opening and closing tags, and an
// id repeats in later prompts, so ids cannot pair tags. Text between two blocks
// cannot be told from a paste that contains a forged closing tag and a forged
// opening tag. A closing tag may carry the same id as its opening tag. A part
// with a closing tag but no opening tag keeps only the text after the last
// closing tag, since its opening tag may have stood in a removed block. Tags
// are matched ignoring case, with white space around the name and in the
// attributes.
//
// It proves nothing about who sent the prompt. The caller must have positive
// proof that the owner typed it, such as the harness's own origin mark.
func OwnerTypedSegments(prompt string) []string {
	var out []string
	for _, part := range typedAroundBlocks(prompt, pastedTagName) {
		out = append(out, typedAroundBlocks(part, reminderTagName)...)
	}
	return out
}

// WithoutSystemReminders returns prompt with every system-reminder block
// removed by the same rule as OwnerTypedSegments. A reminder Claude Code
// appends must not hide the text the owner typed before it.
func WithoutSystemReminders(prompt string) string {
	return strings.Join(typedAroundBlocks(prompt, reminderTagName), " ")
}

// typedAroundBlocks returns the text outside the blocks named name: the part
// before the first opening tag and the part after the last closing tag, with
// empty parts dropped.
func typedAroundBlocks(s, name string) []string {
	first, _ := findTag(s, name, 0, false)
	if first < 0 {
		return trimParts(afterClosingTags([]string{s}, name))
	}
	last, lastEnd := lastTag(s, name, true)
	parts := []string{s[:first]}
	if last >= first && lastEnd >= 0 {
		rest := s[lastEnd:]
		if next, _ := findTag(rest, name, 0, false); next >= 0 {
			rest = rest[:next]
		}
		parts = append(parts, rest)
	}
	return trimParts(afterClosingTags(parts, name))
}

// afterClosingTags drops the text before a closing tag when its opening tag is
// not in the same part. Such an opening tag stood in a removed block, so
// nothing shows that the text before the close was typed; a paste could carry
// a forged close and an opening tag of another kind. Only the text after the
// last closing tag is kept. A closing tag with no '>' hides where its block
// would end, so the whole part is dropped.
func afterClosingTags(parts []string, name string) []string {
	var out []string
	for _, p := range parts {
		at, end := lastTag(p, name, true)
		switch {
		case at < 0:
			out = append(out, p)
		case end < 0:
		default:
			out = append(out, p[end:])
		}
	}
	return out
}

// findTag returns the first tag named name at or after start and the byte
// after its '>', or -1 when there is none. An opening tag without a '>' is
// still an opening tag and returns -1 as its end.
func findTag(s, name string, start int, closing bool) (int, int) {
	for i := start; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		if ok, end := tagAt(s, i, name, closing); ok {
			return i, end
		}
	}
	return -1, -1
}

// lastTag returns the last tag named name in s and the byte after its '>'.
func lastTag(s, name string, closing bool) (int, int) {
	at, end := -1, -1
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		if ok, e := tagAt(s, i, name, closing); ok {
			at, end = i, e
		}
	}
	return at, end
}

// tagAt says whether a tag named name starts at i, and where it ends. A name
// must be followed by white space, '/' or '>', so <pasted_contents> is not a
// match. Case and white space do not matter.
func tagAt(s string, i int, name string, closing bool) (bool, int) {
	j := i + 1
	if closing {
		j = skipSpace(s, j)
		if j >= len(s) || s[j] != '/' {
			return false, 0
		}
		j++
	}
	j = skipSpace(s, j)
	if len(s)-j < len(name) || !strings.EqualFold(s[j:j+len(name)], name) {
		return false, 0
	}
	j += len(name)
	if j < len(s) && !tagBoundary(s[j]) {
		return false, 0
	}
	if end := strings.IndexByte(s[j:], '>'); end >= 0 {
		return true, j + end + 1
	}
	return true, -1
}

func skipSpace(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func tagBoundary(c byte) bool {
	switch c {
	case '>', '/', ' ', '\t', '\n', '\r':
		return true
	}
	return false
}

func trimParts(parts []string) []string {
	var out []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
