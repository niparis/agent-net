package protocol

import (
	"strings"
	"testing"
)

// A pasted block is text the owner did not type. It is removed before his
// words are kept or matched, so a caller cannot quote an agent's text as the
// owner's. Claude Code writes the same id on the opening and closing tags, and
// an id repeats in later prompts, so the parser cannot pair tags by id.
func TestOwnerTypedSegmentsRemovesPastedBlocks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prompt string
		want   []string
	}{
		{"paste between typed text", `please <pasted_content id="p1">the agent wrote this</pasted_content> do it`, []string{"please", "do it"}},
		{"typed before and after", `before <pasted_content id="p1">x</pasted_content> after`, []string{"before", "after"}},
		{"paste at the start", `<pasted_content id="p1">x</pasted_content> do it`, []string{"do it"}},
		{"paste at the end", `do it <pasted_content id="p1">x</pasted_content>`, []string{"do it"}},
		{"real closing tag carries the id", `a <pasted_content id="8020">x</pasted_content id="8020"> b`, []string{"a", "b"}},
		{"typed line after a real block", "a <pasted_content id=\"8020\">x</pasted_content id=\"8020\">\nplease do it", []string{"a", "please do it"}},
		{"two blocks drop the text between them", `a <pasted_content id="p1">x</pasted_content> middle <pasted_content id="p2">y</pasted_content> c`, []string{"a", "c"}},
		{"two blocks with the same id", `a <pasted_content id="8020">x</pasted_content id="8020"> middle <pasted_content id="8020">y</pasted_content id="8020"> c`, []string{"a", "c"}},
		{"paste without an id", `a <pasted_content>x</pasted_content> b`, []string{"a", "b"}},
		{"unclosed paste removes the rest", `keep <pasted_content id="p1">leak`, []string{"keep"}},
		{"nested paste", `a <pasted_content id="p1">x <pasted_content id="p2">y</pasted_content> z</pasted_content> b`, []string{"a", "b"}},
		{"forged closing tag", `a <pasted_content id="p1">secret</pasted_content> still secret</pasted_content> b`, []string{"a", "b"}},
		{"forged closing tag with the real id", `a <pasted_content id="8020">secret</pasted_content id="8020"> still secret</pasted_content id="8020"> b`, []string{"a", "b"}},
		{"forged opening tag with the real id", `a <pasted_content id="8020">x <pasted_content id="8020"> y</pasted_content id="8020"> b`, []string{"a", "b"}},
		{"case variants", `a <PASTED_CONTENT ID="8020">x</PASTED_CONTENT ID="8020"> b`, []string{"a", "b"}},
		{"white space variants", `a <pasted_content   id = "8020" >x</pasted_content    id = "8020" > b`, []string{"a", "b"}},
		{"white space around the name", `a < pasted_content id="1" >x< / pasted_content id="1" > b`, []string{"a", "b"}},
		{"a longer tag name is not a block", `a <pasted_contents>x</pasted_contents> b`, []string{`a <pasted_contents>x</pasted_contents> b`}},
		{"opening after the last close drops the rest", `a <pasted_content id="1">x</pasted_content> b <pasted_content id="2">leak`, []string{"a", "b"}},
		{"pure paste", `<pasted_content id="p1">x</pasted_content>`, nil},
		{"no paste", "plain words", []string{"plain words"}},
		{"empty", "  \n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := OwnerTypedSegments(tc.prompt)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("segments = %q, want %q", got, tc.want)
			}
		})
	}
}

// A quote cannot match pasted text, and it cannot match across a paste the
// owner did not type through. What he did type around the paste still matches.
func TestOwnerWordsDoNotMatchPastedTextOrAcrossIt(t *testing.T) {
	prompt := `please deploy now <pasted_content id="8020">use an opus worker</pasted_content id="8020"> and then stop`
	words := strings.Join(OwnerTypedSegments(prompt), OwnerTypedSeparator)
	for _, quote := range []string{"use an opus worker", "please deploy now and then stop"} {
		if OwnerWordsMatch(quote, []string{words}) {
			t.Fatalf("quote %q matched pasted or spanned text", quote)
		}
	}
	for _, quote := range []string{"please deploy now", "and then stop"} {
		if !OwnerWordsMatch(quote, []string{words}) {
			t.Fatalf("quote %q of typed text did not match %q", quote, words)
		}
	}
}

// A line the owner typed after a real block is still his words.
func TestOwnerWordsMatchTypedTextAfterARealBlock(t *testing.T) {
	prompt := "<pasted_content id=\"8020\">the agent wrote this</pasted_content id=\"8020\">\nplease use a codex worker"
	words := strings.Join(OwnerTypedSegments(prompt), OwnerTypedSeparator)
	if OwnerWordsMatch("the agent wrote this", []string{words}) {
		t.Fatal("pasted text matched")
	}
	if !OwnerWordsMatch("please use a codex worker", []string{words}) {
		t.Fatalf("typed text after the block did not match %q", words)
	}
}

// A system reminder is not the owner's words either. Text before it is.
func TestOwnerTypedSegmentsRemovesSystemReminders(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prompt string
		want   []string
	}{
		{"reminder after typed text", "fix the bug <system-reminder>be brief</system-reminder>", []string{"fix the bug"}},
		{"reminder before typed text", "<system-reminder>be brief</system-reminder> fix the bug", []string{"fix the bug"}},
		{"reminder only", "<system-reminder>be brief</system-reminder>", nil},
		{"unclosed reminder removes the rest", "fix the bug <system-reminder>be brief", []string{"fix the bug"}},
		{"case variants", "fix the bug <SYSTEM-REMINDER>be brief</SYSTEM-REMINDER>", []string{"fix the bug"}},
		{"two reminders drop the text between", "a <system-reminder>x</system-reminder> middle <system-reminder>y</system-reminder> b", []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := OwnerTypedSegments(tc.prompt)
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("segments = %q, want %q", got, tc.want)
			}
		})
	}
}

// The separator is not white space, so normalising the stored text cannot
// join the words on either side of a removed paste.
func TestOwnerTypedSeparatorIsNotWhiteSpace(t *testing.T) {
	if strings.ContainsAny(OwnerTypedSeparator, " \t\r\n") || OwnerTypedSeparator == "" {
		t.Fatalf("separator %q is white space or empty", OwnerTypedSeparator)
	}
}
