package phone

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/operator"
	"github.com/niparis/agent-net/internal/protocol"
)

// The Messages screen reads the same stored conversations as the TUI Messages
// tab: the list with conversations, and the messages of the selected one. The
// owner writes into a thread with the owner send path, as the TUI does.

// conversationPageSize is how many conversations the list holds. The TUI pages
// further; the web shows the newest and says how many are left out.
const conversationPageSize = 50

// threadLimit is how many messages one thread reads, as the TUI does.
const threadLimit = 200

// bodyLimit cuts a very long message for display. The full text is in the TUI.
const bodyLimit = 4000

type convListRow struct {
	ID, Who, Preview, Age string
	Count                 int
	CTO, Worker, Selected bool
}

type threadMsg struct {
	Class, Who, Age, Text, Instruction string
}

type recipient struct{ ID, Name string }

type threadView struct {
	ID, Title, Parties string
	Msgs               []threadMsg
	To                 []recipient
	Err                string
}

type messagesView struct {
	Rows   []convListRow
	More   int
	Thread *threadView
}

const workerNoticeHead = "[agent-net worker notice]"

// noticeLine is a worker's notice in one short line, as the TUI writes it:
// "✓ done · 12m · 3 files". ok is false for a message that is not a notice.
func noticeLine(body string) (text string, ok bool) {
	if !strings.HasPrefix(strings.TrimSpace(body), workerNoticeHead) {
		return "", false
	}
	status, run, files := "", "", ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "status: "):
			status = strings.TrimPrefix(line, "status: ")
		case strings.HasPrefix(line, "run time: "):
			run = strings.TrimPrefix(line, "run time: ")
		case strings.Contains(line, " files changed"), strings.Contains(line, " file changed"):
			files = strings.Fields(line)[0] + " files"
		}
	}
	if status == "" {
		status = "stopped"
	}
	text = "✓ " + status
	if status != "done" && status != "started" && status != "queued" {
		text = "✗ " + status
	}
	if status == "started" || status == "queued" {
		text = status
	}
	for _, part := range []string{run, files} {
		if part != "" {
			text += " · " + part
		}
	}
	return text, true
}

func (a *app) messages(ctx context.Context, snap operator.Snapshot, selected string) *messagesView {
	now := time.Now()
	v := &messagesView{}
	page, ok := a.conversations(ctx)
	if !ok {
		return v
	}
	names, owners := map[string]string{}, map[string]bool{}
	for _, ag := range snap.Agents {
		names[ag.SessionID] = sessionName(ag)
		if protocol.IsOwner(ag.Harness) {
			owners[ag.SessionID] = true
		}
	}
	name := func(id, stored string) string {
		if owners[id] {
			return "you"
		}
		return firstNonEmpty(names[id], stored, "ended session")
	}
	var chosen *protocol.ConversationView
	for i := range page.Conversations {
		if page.Conversations[i].ID == selected {
			chosen = &page.Conversations[i]
		}
	}
	if chosen == nil && len(page.Conversations) > 0 {
		chosen = &page.Conversations[0]
	}
	for _, c := range page.Conversations {
		who := name(c.From, c.FromName) + " ↔ " + name(c.To, c.ToName)
		preview := c.LastLine
		if c.Task != "" {
			preview = c.Task + ": " + c.LastLine
		}
		v.Rows = append(v.Rows, convListRow{ID: c.ID, Who: who, Preview: preview, Age: ago(c.At, now), Count: c.Count,
			CTO: c.FromCTO, Worker: c.WorkerSession != "", Selected: chosen != nil && chosen.ID == c.ID})
	}
	v.More = max(page.Total-len(page.Conversations), 0)
	if chosen != nil {
		v.Thread = a.thread(ctx, *chosen, names, owners, now)
	}
	return v
}

func (a *app) conversations(ctx context.Context) (protocol.ConversationPage, bool) {
	var page protocol.ConversationPage
	raw, err := a.read(ctx, protocol.OpConversations, protocol.QueryConversations{Limit: conversationPageSize})
	if err != nil || json.Unmarshal(raw, &page) != nil {
		return page, false
	}
	return page, true
}

// threadMessages reads the stored messages of one conversation, oldest first.
func (a *app) threadMessages(ctx context.Context, id string) ([]protocol.Message, error) {
	raw, err := a.read(ctx, protocol.OpConversationMessages, protocol.QueryConversationMessages{ConversationID: id, Limit: threadLimit})
	if err != nil {
		return nil, err
	}
	var res protocol.ConversationMessagesResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	msgs := res.Messages
	// The gateway answers newest first.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (a *app) thread(ctx context.Context, c protocol.ConversationView, names map[string]string, owners map[string]bool, now time.Time) *threadView {
	label := func(id, stored string) string {
		if owners[id] {
			return "you"
		}
		return firstNonEmpty(names[id], stored, "ended session")
	}
	t := &threadView{ID: c.ID, Parties: label(c.From, c.FromName) + " ↔ " + label(c.To, c.ToName)}
	t.Title = firstNonEmpty(c.Task, t.Parties)
	// The owner writes to the sessions of the conversation that are not the owner.
	seen := map[string]bool{}
	for _, id := range []string{c.WorkerSession, c.To, c.From} {
		if id != "" && !seen[id] && !owners[id] {
			seen[id] = true
			stored := c.ToName
			if id == c.From {
				stored = c.FromName
			}
			t.To = append(t.To, recipient{ID: id, Name: label(id, stored)})
		}
	}
	msgs, err := a.threadMessages(ctx, c.ID)
	if err != nil {
		t.Err = "The messages could not be read: " + firstLine(err.Error())
		return t
	}
	for _, m := range msgs {
		tm := threadMsg{Class: "", Who: label(m.From, m.FromName), Age: ago(m.CreatedAt, now), Text: m.Body, Instruction: m.OwnerInstruction}
		switch {
		case owners[m.From] || m.FromHarness == protocol.OwnerHarness:
			tm.Class, tm.Who = " me", "you"
		case m.FromCTO:
			tm.Class, tm.Who = " cto", "CTO"
		}
		if line, ok := noticeLine(m.Body); ok {
			tm.Class, tm.Text = " sys", line
		} else if len([]rune(tm.Text)) > bodyLimit {
			tm.Text = string([]rune(tm.Text)[:bodyLimit]) + "\n... cut here, the TUI has the whole message"
		}
		if strings.TrimSpace(tm.Text) == "" {
			tm.Text = "empty message"
			tm.Class = " sys"
		}
		t.Msgs = append(t.Msgs, tm)
	}
	return t
}

// replier is the owner send into a conversation. The daemon client has it. A
// source without it sends a plain owner message, which opens a conversation.
type replier interface {
	ReplyAsOwner(ctx context.Context, conversationID, to, body string) (protocol.MessageSent, error)
}

// reply sends the owner's text to one session of a conversation, with the owner
// send of the TUI and the conversation id, so the gateway keeps the message in
// that conversation. The session must have written or received a message in the
// thread the owner sees, so a reply cannot be a message to anyone else. The
// thread of a worker row is a group of conversations: the reply goes to the
// newest one that has that session as a side.
func (a *app) reply(ctx context.Context, conversation, to, text string) error {
	text = strings.TrimSpace(text)
	switch {
	case to == "":
		return fmt.Errorf("no recipient")
	case text == "":
		return fmt.Errorf("the message is empty")
	case len(text) > protocol.MaxBodyBytes:
		return fmt.Errorf("the message is longer than %d bytes", protocol.MaxBodyBytes)
	}
	msgs, err := a.threadMessages(ctx, conversation)
	if err != nil {
		return fmt.Errorf("conversation not found")
	}
	target := ""
	for _, m := range msgs {
		if (m.From == to || m.To == to) && m.ConversationID != "" {
			target = m.ConversationID
		}
	}
	if target == "" {
		return fmt.Errorf("that session is not part of the conversation")
	}
	if r, ok := a.src.(replier); ok {
		_, err = r.ReplyAsOwner(ctx, target, to, text)
	} else {
		_, err = a.src.SendAsOwner(ctx, to, text)
	}
	return err
}
