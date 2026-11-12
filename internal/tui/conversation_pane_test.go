package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// messageConversationText is a whole thread row, both lines, as one string.
func (m Model) messageConversationText(c messageConversation, w int, selected bool) string {
	return strings.Join(m.messageConversationRow(c, w, selected), "\n")
}

// paneText is the right-hand panel of a wide frame, one entry per row, with
// the border cut off. It is the conversation pane as the owner reads it.
func paneText(t *testing.T, m Model) []string {
	t.Helper()
	var out []string
	inPane := false
	for _, line := range frameLines(m) {
		i := strings.Index(line, "││")
		if i < 0 {
			i = strings.Index(line, "╮╭")
			if i < 0 {
				continue
			}
		}
		right := line[i+len("││"):]
		if strings.Contains(line, "╭─Conversation") {
			inPane = true
		}
		if !inPane {
			continue
		}
		right = strings.TrimSuffix(strings.TrimRight(right, " "), "│")
		out = append(out, strings.TrimRight(right, " "))
	}
	return out
}

func TestThreadPaneShowsADateSeparatorWhenTheDayChanges(t *testing.T) {
	m := readingScene(t, 160, 45)
	var seps []string
	for _, l := range paneText(t, m) {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "─") && strings.HasSuffix(l, "─") {
			seps = append(seps, l)
		}
	}
	if len(seps) != 1 || !strings.Contains(seps[0], "Thu 29 Oct") {
		t.Fatalf("want one separator for the second day, got %q\n%s", seps, screen(m))
	}
}

func TestThreadPaneOpensAtTheNewestMessageAndMarksOlderAbove(t *testing.T) {
	m := readingScene(t, 120, 24)
	v := screen(m)
	if !strings.Contains(v, "Merged. Thanks for the quick review.") {
		t.Fatalf("the newest message is not in view:\n%s", v)
	}
	if strings.Contains(v, "Can you review the registration migration") {
		t.Fatalf("the oldest message is in view on a short pane:\n%s", v)
	}
	if !strings.Contains(v, "older above") {
		t.Fatalf("no older-above mark:\n%s", v)
	}
	tall := screen(readingScene(t, 160, 45))
	if strings.Contains(tall, "older above") {
		t.Fatalf("older-above mark shown when the whole thread fits:\n%s", tall)
	}
}

func TestThreadPaneOpensAtTheNewestMessageAfterTheFeedCursorMoved(t *testing.T) {
	m := readingScene(t, 120, 24)
	m.feedCur = 0 // an unrelated feed row, as left behind by another thread
	m.threadScrolled = false
	if v := screen(m); !strings.Contains(v, "Merged. Thanks for the quick review.") {
		t.Fatalf("the newest message is not in view:\n%s", v)
	}
}

var leadingSGR = regexp.MustCompile(`((?:\x1b\[[0-9;:]*m)+)$`)

// senderStyle is the escape sequence directly in front of a sender's name on
// each header line that names them.
func senderStyle(t *testing.T, lines []string, name string) []string {
	t.Helper()
	var out []string
	for _, line := range lines {
		plain := ansi.Strip(line)
		if !strings.HasPrefix(strings.TrimLeft(plain, "▸ "), "2") && !strings.Contains(plain, " "+name) {
			continue
		}
		i := strings.Index(line, name)
		if i < 0 || !regexp.MustCompile(`\d\d:\d\d `).MatchString(plain) {
			continue
		}
		if m := leadingSGR.FindString(line[:i]); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func TestThreadPaneKeepsOneColourPerSenderInAThread(t *testing.T) {
	old := testColorProfile
	defer setTestColorProfile(old)
	setTestColorProfile(termenv.TrueColor)
	m := readingScene(t, 160, 45)
	rows := m.messageConversations()
	chat, _ := m.messageThread(rows, m.selectedConversationIndex(rows), 100)
	strats := senderStyle(t, chat, "tenebrae-strats")
	teneb := senderStyle(t, chat, "tenebrae 3")
	if len(strats) != 3 || len(teneb) != 2 {
		t.Fatalf("headers found: strats %d, teneb %d\n%s", len(strats), len(teneb), strings.Join(chat, "\n"))
	}
	for _, s := range strats[1:] {
		if s != strats[0] {
			t.Fatalf("one sender, two colours: %q vs %q", s, strats[0])
		}
	}
	for _, s := range teneb[1:] {
		if s != teneb[0] {
			t.Fatalf("one sender, two colours: %q vs %q", s, teneb[0])
		}
	}
	if strats[0] == teneb[0] {
		t.Fatalf("two senders share a colour: %q", strats[0])
	}
}

func TestThreadRowsTakeTwoLines(t *testing.T) {
	m := readingScene(t, 120, 34)
	rows := m.messageConversations()
	lines, sel := m.threadListLines(rows, 50, 0, 20)
	if len(lines) != 2*len(rows) {
		t.Fatalf("want two lines per row, got %d lines for %d rows:\n%s", len(lines), len(rows), strings.Join(lines, "\n"))
	}
	if sel != 1 {
		t.Fatalf("the selected row's last line is the one to keep in view, got %d", sel)
	}
	first, second := ansi.Strip(lines[0]), ansi.Strip(lines[1])
	if !strings.Contains(first, "tenebrae-strats") || !strings.Contains(first, "2m ago") {
		t.Fatalf("first line has the names and the time: %q", first)
	}
	if strings.Contains(first, "Merged") {
		t.Fatalf("the last line of text belongs on the second line: %q", first)
	}
	if !strings.Contains(second, "Merged. Thanks for the quick review.") {
		t.Fatalf("second line has the last line of text: %q", second)
	}
	if old := ansi.Strip(lines[len(lines)-2]); !strings.Contains(old, "1d ago") {
		t.Fatalf("a day-old thread reads in days: %q", old)
	}
}

func TestThreadListIsNewestFirst(t *testing.T) {
	m := readingScene(t, 120, 34)
	var got []string
	for _, c := range m.messageConversations() {
		got = append(got, c.id)
	}
	want := []string{readingThreadID, "c_ops", "c_waiting", "c_old"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("threads are not newest first: got %v want %v", got, want)
	}
}

func TestThreadPaneMessageLayout(t *testing.T) {
	m := readingScene(t, 160, 45)
	pane := paneText(t, m)
	find := func(prefix string) int {
		for i, l := range pane {
			if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "▸")), prefix) {
				return i
			}
		}
		t.Fatalf("no line starting %q in:\n%s", prefix, strings.Join(pane, "\n"))
		return -1
	}
	h3 := find("29 Oct 09:05")
	if !strings.Contains(pane[h3], "tenebrae-strats") {
		t.Fatalf("the header has day, time and sender: %q", pane[h3])
	}
	// The links sit on one dim line under the header, before the body.
	links := pane[h3+1]
	for _, want := range []string{"m_3", "ch_8c3bc9d80f8dd4aa", "ask i_4d2a91c0d4e5", "PR #182"} {
		if !strings.Contains(links, want) {
			t.Fatalf("links line is missing %q: %q", want, links)
		}
	}
	body := pane[h3+2]
	if !strings.HasPrefix(body, "    ") || !strings.Contains(body, "The migration is ready") {
		t.Fatalf("the body is indented under its header: %q", body)
	}
	// A reply names what it answers.
	h4 := find("29 Oct 13:30")
	if re := pane[h4+1]; !strings.Contains(re, "re:") || !strings.Contains(re, "tenebrae-strats") || !strings.Contains(re, "The migration is ready") {
		t.Fatalf("the reply has no re: marker naming its parent: %q", re)
	}
	// A blank line separates messages.
	if strings.TrimSpace(pane[h4-1]) != "" {
		t.Fatalf("no blank line before the next message: %q", pane[h4-1])
	}
}

func TestThreadPaneWrapsWithinTheWidthAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {160, 45}} {
		m := readingScene(t, size[0], size[1])
		for i, line := range frameLines(m) {
			if w := ansi.StringWidth(line); w > size[0] {
				t.Errorf("%dx%d row %d is %d columns wide: %q", size[0], size[1], i, w, line)
			}
		}
	}
	// No word is cut: the long body reads back whole once the wrapping is undone.
	m := readingScene(t, 120, 60)
	joined := strings.Join(strings.Fields(strings.Join(paneText(t, m), " ")), " ")
	want := "This paragraph is long on purpose so the conversation pane has to wrap it over several lines and never cut a word in the middle or run past the edge of the pane."
	if !strings.Contains(joined, want) {
		t.Fatalf("the long body was cut or lost:\n%s", joined)
	}
}

func TestMessagesTabCTOStripIsSmall(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 34}, {160, 45}} {
		m := readingScene(t, size[0], size[1])
		if h := m.ctoBoxHeight(messagesTab); h > 4 {
			t.Errorf("%dx%d: the CTO strip takes %d rows", size[0], size[1], h)
		}
	}
}
