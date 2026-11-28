package phone

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/protocol"
)

// The CTO panel reads the same timeline as the TUI CTO pane: cto_settings for
// the title, harness and model, and cto_events for the conversation and its
// older pages. It sends through cto_send, the owner call of the TUI. Model
// text is text: it is never HTML, and markdown stays as typed.

// ctoLine is one rendered line of the conversation. Class is " me", " cto" or
// " sys", like the Messages thread.
type ctoLine struct {
	Class, Who, Age, Text string
}

type ctoView struct {
	Title, Harness, Model, Chip string
	Err                         string
	Lines                       []ctoLine
	Older                       int
	OlderHref, LatestHref       string
}

// ctoSettings reads the owner's CTO name, harness and model. It changes
// nothing: the TUI keeps the settings form.
func (a *app) ctoSettings(ctx context.Context) protocol.CTOSettings {
	var s protocol.CTOSettings
	raw, err := a.read(ctx, protocol.OpCTOSettings, nil)
	if err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// cto reads one page of the conversation. h is the offset of an older page,
// or zero for the newest one.
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
	raw, err := a.read(ctx, protocol.OpCTOEvents, protocol.CTOEventsRequest{Index: index})
	if err != nil {
		v.Err = "The conversation could not be read: " + firstLine(err.Error())
	} else {
		var events protocol.CTOEvents
		if json.Unmarshal(raw, &events) == nil {
			v.Lines = ctoLines(events, v.Title, now)
			v.Older = events.Older
		}
	}

	return v
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
