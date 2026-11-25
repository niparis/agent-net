package claudeframes_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/niparis/agent-net/internal/claudeframes"
	"github.com/niparis/agent-net/internal/daemon/claude"
	"github.com/niparis/agent-net/internal/protocol"
)

const testSock = "/tmp/agent-net-501/r/s_bob.c_6f1b93f3c3bf060c.sock"

// builtFrame writes one frame with the production builder and describes it as
// Claude Code reports it in a hold notice.
func builtFrame(t *testing.T, m protocol.Message) claudeframes.Description {
	t.Helper()
	line, err := claude.BuildFrameFor(m, testSock, false)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &f); err != nil {
		t.Fatal(err)
	}
	d, ok := claudeframes.Describe(f.Message.Content)
	if !ok {
		t.Fatalf("the built frame has no wrapper: %s", f.Message.Content)
	}
	return d
}

// noticeText is a hold notice of the real shape, with the preview and counts
// of the frame it held.
func noticeText(d claudeframes.Description, lines, chars int) string {
	return "Held peer message \u2014 from uds:" + testSock + " (peer claims name: bob); preview: \u00ab" + d.Preview +
		fmt.Sprintf("\u00bb \u2026[%d lines, %d chars total \u2014 expand to review before approving] \u2014 not delivered to Claude (1 held). ", lines, chars) +
		"The sending session's permission mode class doesn't match this session's. Review it below, or set \"crossSessionInbound\" to \"accept\"."
}

func msg(id, body string) protocol.Message {
	return protocol.Message{ID: id, From: "s_bob", FromName: "bob", FromHarness: "pi",
		ConversationID: "c_6f1b93f3c3bf060c", Type: protocol.TypeRequest, Body: body}
}

// One notice of each real shape names its frame by the reply socket and the
// inner frame's line and character counts.
func TestHeldNoticeOfTheRealShapeNamesItsFrame(t *testing.T) {
	long := strings.Repeat("a long first line ", 20)
	reminder := msg("m_0000000000000010", "[agent-net] You still owe bob an answer to m_0123456789abcdef.")
	reminder.From, reminder.FromHarness, reminder.Type = "agent-net", "agent-net", protocol.TypeNotification
	worker := msg("m_0000000000000011", "[agent-net worker notice] hand-back (quiet) for 'the owner's task'")
	reply := msg("m_0000000000000012", "all done")
	reply.Type = protocol.TypeReply
	batch := msg("m_0000000000000013", "2 messages arrived while you were busy.\n\n--- 1 of 2 ---\nx")
	batch.Batch = []protocol.Message{msg("m_0000000000000001", "x"), msg("m_0000000000000002", "y")}

	cases := []struct {
		name string
		m    protocol.Message
	}{
		{"long first line", msg("m_0000000000000001", long)},
		{"worker notice with brackets and an apostrophe", worker},
		{"reminder from agent-net", reminder},
		{"a reply", reply},
		{"a batch carrier", batch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := builtFrame(t, tc.m)
			n, ok := claudeframes.ParseNotice(noticeText(d, d.Lines, d.Chars))
			if !ok || n.ReplySock != testSock {
				t.Fatalf("notice = %+v ok=%v", n, ok)
			}
			if got := claudeframes.Choose(n, []claudeframes.Description{d}); got != 0 {
				t.Fatalf("Choose = %d, want the only frame", got)
			}
		})
	}
}

// Two frames on one socket can have the same size. The quoted first line then
// picks the one, with the characters Claude Code drops removed.
func TestHeldNoticeBreaksASizeTieWithThePreview(t *testing.T) {
	one := builtFrame(t, msg("m_0000000000000001", "first (a)\nxx"))
	two := builtFrame(t, msg("m_0000000000000002", "other [b]\nxx"))
	if one.Lines != two.Lines || one.Chars != two.Chars {
		t.Fatalf("the frames do not have the same size: %+v %+v", one, two)
	}
	n, ok := claudeframes.ParseNotice(noticeText(two, two.Lines, two.Chars))
	if !ok {
		t.Fatal("the notice was not parsed")
	}
	if got := claudeframes.Choose(n, []claudeframes.Description{one, two}); got != 1 {
		t.Fatalf("Choose = %d, want the frame whose preview is quoted", got)
	}
}

// A character outside the BMP counts as one code point and two UTF-16 units.
// A notice from either reader names the frame.
func TestHeldNoticeAcceptsCodePointsAndUTF16(t *testing.T) {
	d := builtFrame(t, msg("m_0000000000000001", "deploy \U0001F680 now"))
	if d.Chars16 != d.Chars+1 {
		t.Fatalf("counts = %d code points, %d UTF-16 units, want one more", d.Chars, d.Chars16)
	}
	for _, chars := range []int{d.Chars, d.Chars16} {
		n, ok := claudeframes.ParseNotice(noticeText(d, d.Lines, chars))
		if !ok {
			t.Fatal("the notice was not parsed")
		}
		if got := claudeframes.Choose(n, []claudeframes.Description{d}); got != 0 {
			t.Fatalf("count %d: Choose = %d, want the frame", chars, got)
		}
	}
}

// A notice with no counts, or counts that match no frame, is not guessed.
func TestHeldNoticeWithoutAMatchMarksNothing(t *testing.T) {
	d := builtFrame(t, msg("m_0000000000000001", "the body"))
	n, _ := claudeframes.ParseNotice(noticeText(d, d.Lines, d.Chars))
	n.Lines, n.Chars = 0, 0
	if got := claudeframes.Choose(n, []claudeframes.Description{d}); got != -1 {
		t.Fatalf("a notice without counts chose %d", got)
	}
	n, _ = claudeframes.ParseNotice(noticeText(d, d.Lines+1, d.Chars+1))
	if got := claudeframes.Choose(n, []claudeframes.Description{d}); got != -1 {
		t.Fatalf("a notice with other counts chose %d", got)
	}
}
