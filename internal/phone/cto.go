package phone

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO panel reads the same timeline as the TUI CTO pane: cto_settings for
// the title, harness and model, cto_events for the conversation and its older
// pages, and proposal_list for what waits for the owner. It sends through
// cto_send and answers a proposal with proposal_answer, the owner calls of the
// TUI. Model text is text: it is never HTML, and markdown stays as typed.

// ctoLine is one rendered line of the conversation. Class is " me", " cto" or
// " sys", like the Messages thread.
type ctoLine struct {
	Class, Who, Age, Text string
}

// ctoProposal is one open proposal as a card. Node routes the answer to the
// machine that holds the proposal; it is encoded, never printed.
type ctoProposal struct {
	ID, Node, Summary, Reason, Age string
}

type ctoView struct {
	Title, Harness, Model, Chip string
	Err                         string
	Lines                       []ctoLine
	Proposals                   []ctoProposal
	Older                       int
	OlderHref, LatestHref       string
}

// ctoSettings reads the owner's CTO name, harness and model. It changes
// nothing: the TUI keeps the settings form.
func (a *app) ctoSettings(ctx context.Context) protocol.CTOSettings {
	var s protocol.CTOSettings
	raw, err := a.src.Call(ctx, protocol.OpCTOSettings, nil)
	if err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// cto reads one page of the conversation and the open proposals. h is the
// offset of an older page, or zero for the newest one.
func (a *app) cto(ctx context.Context, h int) *ctoView {
	now := time.Now()
	v := &ctoView{Title: protocol.DefaultCTOTitle}
	s := a.ctoSettings(ctx)
	v.Title = protocol.CTOTitleOrDefault(s.Title)
	v.Harness = firstNonEmpty(s.Harness, "opencode")
	v.Model = firstNonEmpty(s.Model, "default")
	v.Chip = v.Harness + " · " + v.Model

	index := 0
	if h > 0 {
		index = -h
	}
	raw, err := a.src.Call(ctx, protocol.OpCTOEvents, protocol.CTOEventsRequest{Index: index})
	if err != nil {
		v.Err = "The conversation could not be read: " + firstLine(err.Error())
	} else {
		var events protocol.CTOEvents
		if json.Unmarshal(raw, &events) == nil {
			v.Lines = ctoLines(events, v.Title, now)
			v.Older = events.Older
		}
	}

	raw, err = a.src.Call(ctx, protocol.OpProposalList, nil)
	if err != nil {
		if v.Err == "" {
			v.Err = "The proposals could not be read: " + firstLine(err.Error())
		}
	} else {
		var items []protocol.CTOProposal
		if json.Unmarshal(raw, &items) == nil {
			for _, p := range items {
				if p.Status != "open" {
					continue
				}
				v.Proposals = append(v.Proposals, ctoProposal{
					ID: p.ID, Node: encodeProposalNode(p.NodeID), Summary: proposalSummary(p),
					Reason: strings.TrimSpace(p.Reason), Age: ago(p.CreatedAt, now),
				})
			}
		}
	}
	return v
}

// encodeProposalNode keeps a raw node ID out of the page. The form posts this
// value back, and the handler decodes it.
func encodeProposalNode(node string) string {
	if node == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(node))
}

func decodeProposalNode(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("invalid proposal target")
	}
	return string(b), nil
}

// ctoLineTime turns one timeline entry into an order key in nanoseconds.
type ctoTimedLine struct {
	at   int64
	line ctoLine
}

// ctoLines merges messages and actions into one timeline, oldest first, as the
// TUI pane does. A harness failure becomes an error line.
func ctoLines(events protocol.CTOEvents, title string, now time.Time) []ctoLine {
	var rows []ctoTimedLine
	for _, raw := range events.Messages {
		role, text, at, isErr := ctoMessage(raw)
		if text == "" {
			continue
		}
		line := ctoLine{Text: text}
		if isErr {
			line.Class = " sys"
		} else {
			switch role {
			case "user":
				line.Class, line.Who = " me", "you"
			case "assistant":
				line.Class, line.Who = " cto", title
			default:
				continue
			}
		}
		if at > 0 {
			line.Age = ago(time.Unix(0, at), now)
		}
		rows = append(rows, ctoTimedLine{at: at, line: line})
	}
	for _, action := range events.Actions {
		if strings.TrimSpace(action.Text) == "" {
			continue
		}
		line := ctoLine{Class: " sys", Text: action.Text}
		if action.At > 0 {
			line.Age = ago(time.Unix(0, action.At), now)
		}
		rows = append(rows, ctoTimedLine{at: action.At, line: line})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at < rows[j].at })
	out := make([]ctoLine, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.line)
	}
	return out
}

// ctoMessage reads one stored timeline message. It accepts the shapes the
// harnesses store: a plain text field, a content list, or parts.
func ctoMessage(raw json.RawMessage) (role, text string, at int64, isErr bool) {
	var v struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
		Parts   json.RawMessage `json:"parts"`
		Message struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Info struct {
			Role string `json:"role"`
		} `json:"info"`
		Error json.RawMessage `json:"error"`
		Time  struct {
			Created int64 `json:"created"`
		} `json:"time"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return "", "", 0, false
	}
	at = v.Time.Created * int64(time.Millisecond)
	if len(v.Error) > 0 && string(v.Error) != "null" {
		var e struct {
			Message string `json:"message"`
			Name    string `json:"name"`
			Data    struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		msg := ""
		if json.Unmarshal(v.Error, &e) == nil {
			msg = firstNonEmpty(e.Message, e.Data.Message, e.Name)
		}
		if msg == "" {
			msg = strings.Trim(string(v.Error), `"`)
		}
		return "", "error: " + cleanText(msg, true), at, true
	}
	role = firstNonEmpty(v.Type, v.Role, v.Message.Role, v.Info.Role)
	text = firstNonEmpty(v.Text, ctoContentText(v.Content), ctoContentText(v.Message.Content), ctoContentText(v.Parts))
	text = cleanText(text, true)
	if strings.TrimSpace(text) == "" {
		text = ""
	}
	return role, text, at, false
}

// ctoContentText joins the text parts of a content or parts value. A plain
// string is used as it is.
func ctoContentText(raw json.RawMessage) string {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Text != "" && p.Type == "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// cleanText removes control characters and text-direction overrides. Line
// breaks stay: the panel shows the model's text as typed.
func cleanText(s string, keepLines bool) string {
	if keepLines {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
	} else {
		s = strings.ReplaceAll(s, "\r\n", " ")
		s = strings.ReplaceAll(s, "\n", " ")
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			if keepLines {
				return r
			}
			return ' '
		case unicode.IsControl(r):
			return -1
		case unicode.Is(unicode.Cf, r) && (r >= 0x200b && r <= 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069):
			return -1
		}
		return r
	}, s)
}

// proposalSummary is the one-line words on a proposal card, as the TUI shows
// it: the call, not the raw arguments.
func proposalSummary(p protocol.CTOProposal) string {
	var a map[string]any
	_ = json.Unmarshal(p.Args, &a)
	value := func(k string) string {
		v, _ := a[k].(string)
		return cleanText(v, false)
	}
	if (p.Tool == "session_change" && p.Action == "session_start") || (p.Tool == "project_change" && p.Action == "create") {
		project := firstNonEmpty(value("project"), value("path"))
		// Never print the machine's node ID: say that it is a machine (item 9).
		machine := "here"
		if value("node_id") != "" {
			machine = "a machine"
		}
		harness := firstNonEmpty(value("harness"), "claude")
		folder := firstNonEmpty(value("folder"), "project folder")
		message := []rune(strings.Join(strings.Fields(value("first_message")), " "))
		first := ""
		if len(message) > 0 {
			if len(message) > 28 {
				message = append(message[:27], '…')
			}
			first = " · " + string(message)
		}
		verb := "start"
		if p.Action == "create" {
			verb = "create"
		}
		return fmt.Sprintf("%s %s @ %s · %s · %s%s", verb, project, machine, harness, folder, first)
	}
	if p.Tool == "project_remove" || (p.Tool == "project_change" && p.Action == "remove") {
		project := firstNonEmpty(value("name"), value("path"))
		machine := "here"
		if value("node_id") != "" {
			machine = "a machine"
		}
		return fmt.Sprintf("remove project %s @ %s", project, machine)
	}
	switch p.Action {
	case "charter_set":
		return strings.TrimSpace(fmt.Sprintf("charter %s %s", value("node"), value("attr")))
	case "edge_set":
		return fmt.Sprintf("edge %s -> %s rule %s", value("from"), value("to"), value("rule"))
	}
	if p.Tool == "machine_remove" || (p.Tool == "settings_change" && p.Action == "machine_remove") {
		return "remove a machine and revoke its token"
	}
	if p.Tool == "machine_rotate" || (p.Tool == "settings_change" && p.Action == "machine_rotate") {
		return "rotate the token of a machine"
	}
	if p.Tool == "node_rename" || (p.Tool == "settings_change" && p.Action == "machine_name") {
		return "rename a machine to " + value("name")
	}
	if p.Tool == "session_change" {
		return "session " + p.Action
	}
	return strings.TrimSpace(p.Tool + " " + p.Action)
}
