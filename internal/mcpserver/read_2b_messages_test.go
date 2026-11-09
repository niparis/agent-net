package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Pages have no overlap and no gap: three pages of 25, 25 and 10 cover the 60
// matching messages once each, in the same order.
func TestMessagesFindPagesHaveNoOverlapOrGap(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	cs := connect(t, f.call)

	var got []string
	cursor := ""
	for page := 0; page < 3; page++ {
		out := roCall(t, cs, "agent_messages_find", map[string]any{"limit": 25, "cursor": cursor})
		got = append(got, roPageIDs(t, out)...)
		if total := int(out["total"].(float64)); total != 60 {
			t.Fatalf("page %d total = %d, want 60", page, total)
		}
		next, _ := out["next_cursor"].(string)
		if page < 2 {
			if next == "" {
				t.Fatalf("page %d has no next_cursor", page)
			}
			if note, _ := out["note"].(string); !strings.Contains(note, "next_cursor") {
				t.Errorf("page %d has no page-cut note: %q", page, note)
			}
		} else if next != "" {
			t.Fatalf("last page has next_cursor %q", next)
		}
		cursor = next
	}
	if len(got) != 60 {
		t.Fatalf("got %d messages over three pages", len(got))
	}
	seen := map[string]bool{}
	for i, id := range got {
		if seen[id] {
			t.Fatalf("message %s appears twice", id)
		}
		seen[id] = true
		if want := fmt.Sprintf("m_%02d", 59-i); id != want {
			t.Fatalf("page item %d = %s, want %s (newest first, no gap)", i, id, want)
		}
	}
}

// A cursor is bound to the caller and the filters. Reusing it with another
// filter is refused with an actionable error.
func TestMessageCursorRefusedWithOtherFilters(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	cs := connect(t, f.call)
	out := roCall(t, cs, "agent_messages_find", map[string]any{"limit": 5, "direction": "out"})
	cursor, _ := out["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("no cursor")
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_messages_find",
		Arguments: map[string]any{"limit": 5, "direction": "in", "cursor": cursor}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("a cursor from another query was accepted: %+v", res.StructuredContent)
	}
	text := ""
	for _, content := range res.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text += item.Text
		}
	}
	if !strings.Contains(strings.ToLower(text), "cursor") {
		t.Fatalf("the error does not mention the cursor: %q", text)
	}
}

// agent_message_get returns a 20 KB body in UTF-8-safe chunks that join back to
// the original bytes.
func TestMessageGetBodyChunksKeepUTF8(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	unit := "aé日🙂z"
	body := strings.Repeat(unit, 4000)
	if len(body) < 20*1024 {
		t.Fatalf("fixture body is only %d bytes", len(body))
	}
	f.message = roMessageAt("m_big", time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC), "s_peer", "s_me", body)
	cs := connect(t, f.call)

	var joined strings.Builder
	cursor := ""
	chunks := 0
	for {
		args := map[string]any{"message": "m_big", "max_bytes": 257}
		if cursor != "" {
			args["body_cursor"] = cursor
		}
		out := roCall(t, cs, "agent_message_get", args)
		msg := out["message"].(map[string]any)
		chunk := msg["body"].(string)
		if len(chunk) > 257 {
			t.Fatalf("chunk %d is %d bytes, over max_bytes", chunks, len(chunk))
		}
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %d splits a UTF-8 character", chunks)
		}
		joined.WriteString(chunk)
		truncated := out["body_truncated"].(bool)
		next, _ := out["next_body_cursor"].(string)
		if truncated != (next != "") {
			t.Fatalf("chunk %d: body_truncated=%v with next cursor %q", chunks, truncated, next)
		}
		chunks++
		if next == "" {
			break
		}
		cursor = next
		if chunks > 200 {
			t.Fatal("body chunking does not end")
		}
	}
	if joined.String() != body {
		t.Fatalf("the chunks do not join back to the body: %d bytes, want %d", joined.Len(), len(body))
	}
	if chunks < 2 {
		t.Fatalf("a 20 KB body came in %d chunk(s)", chunks)
	}
}

// A body cursor from one message is refused for another message.
func TestBodyCursorBoundToItsMessage(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	f.message = roMessageAt("m_big", time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC), "s_peer", "s_me", strings.Repeat("word ", 4000))
	cs := connect(t, f.call)
	out := roCall(t, cs, "agent_message_get", map[string]any{"message": "m_big", "max_bytes": 256})
	cursor, _ := out["next_body_cursor"].(string)
	if cursor == "" {
		t.Fatal("no body cursor")
	}
	f.message = roMessageAt("m_other", time.Date(2026, 1, 1, 11, 0, 1, 0, time.UTC), "s_peer", "s_me", "short")
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "agent_message_get",
		Arguments: map[string]any{"message": "m_other", "body_cursor": cursor}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("a body cursor from another message was accepted")
	}
}

// agent_message_get shows the gateway's cited message notes next to the body.
func TestMessageGetShowsCitedNotes(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SESSION_ID", "n_me")
	f := roReadFixture()
	f.message = roMessageAt("m_fwd", time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC), "s_peer", "s_me", "see m_c0f041a063cd1359")
	f.message.CitedNotes = []string{"cited m_c0f041a063cd1359: from a (session s_a) to b at 2026-01-01 10:00 UTC, request: \"ok\""}
	cs := connect(t, f.call)
	msg := roCall(t, cs, "agent_message_get", map[string]any{"message": "m_fwd"})["message"].(map[string]any)
	notes, _ := msg["notes"].([]any)
	if len(notes) != 1 || notes[0] != f.message.CitedNotes[0] || msg["body"] != "see m_c0f041a063cd1359" {
		t.Fatalf("message: %v", msg)
	}
}
