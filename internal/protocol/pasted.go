package protocol

import "strings"

// OwnerTypedSeparator stands where a pasted block was removed from the owner's
// prompt. It is not white space, so OwnerWordsMatch cannot join the words on
// either side of a paste into one quote.
const OwnerTypedSeparator = "\x00"

const (
	pastedTagOpen  = "<pasted_content"
	pastedTagClose = "</pasted_content>"
)

// OwnerTypedSegments returns the parts of a prompt the owner typed, in order,
// with every pasted block removed. A block that is never closed drops the rest
// of the prompt. A nested opening tag or a forged closing tag inside a paste
// stays pasted: it cannot end the block early and let text through.
//
// It proves nothing about who sent the prompt. The caller must have positive
// proof that the owner typed it, such as the harness's own origin mark.
func OwnerTypedSegments(prompt string) []string {
	var out []string
	for {
		i := strings.Index(prompt, pastedTagOpen)
		if i < 0 {
			if s := strings.TrimSpace(prompt); s != "" {
				out = append(out, s)
			}
			return out
		}
		if s := strings.TrimSpace(prompt[:i]); s != "" {
			out = append(out, s)
		}
		end := pastedBlockEnd(prompt, i)
		if end < 0 {
			return out
		}
		prompt = prompt[end:]
	}
}

// pastedBlockEnd returns the byte after the closing tag of the block that
// starts at start, or -1 when the block is never closed.
func pastedBlockEnd(s string, start int) int {
	depth := 1
	pos := start + len(pastedTagOpen)
	for {
		open := strings.Index(s[pos:], pastedTagOpen)
		closeAt := strings.Index(s[pos:], pastedTagClose)
		if closeAt < 0 {
			return -1
		}
		if open >= 0 && open < closeAt {
			depth++
			pos += open + len(pastedTagOpen)
			continue
		}
		end := pos + closeAt + len(pastedTagClose)
		// A close followed by another close before the next open is text
		// inside the paste, not its end: a real block has one closing tag.
		if depth == 1 && laterCloseBeforeOpen(s, end) {
			pos = end
			continue
		}
		depth--
		if depth == 0 {
			return end
		}
		pos = end
	}
}

// laterCloseBeforeOpen tells whether another closing tag comes before the next
// opening tag, with only pasted text between them.
func laterCloseBeforeOpen(s string, pos int) bool {
	open := strings.Index(s[pos:], pastedTagOpen)
	closeAt := strings.Index(s[pos:], pastedTagClose)
	return closeAt >= 0 && (open < 0 || closeAt < open)
}
