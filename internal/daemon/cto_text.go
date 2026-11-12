package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// The owner's CTO timeline is read through session_text on the CTO session, so
// the TUI and the phone use the same owner method as for any other session.
// Each entry carries the text the box shows; graph actions carry the
// cto_action tool name, and a harness failure carries cto_error.
func (d *Daemon) ctoTextPage(ctx context.Context, q protocol.TextQuery) (protocol.TextPage, error) {
	index := 0
	if q.Before != "" {
		n, err := strconv.Atoi(q.Before)
		if err != nil || n < 0 {
			return protocol.TextPage{}, errors.New("bad CTO timeline cursor")
		}
		index = -n
	}
	events, err := d.ctoEvents(ctx, index)
	if err != nil {
		return protocol.TextPage{}, err
	}
	type item struct {
		at         int64
		role, text string
		tool       string
	}
	var items []item
	for _, raw := range events.Messages {
		role, text, tool, at, ok := ctoTextEntry(raw)
		if ok {
			items = append(items, item{at: at, role: role, text: text, tool: tool})
		}
	}
	for _, a := range events.Actions {
		items = append(items, item{at: a.At, role: protocol.RoleAssistant, text: a.Text, tool: "cto_action"})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at < items[j].at })
	limit := clampLimit(q.Limit)
	drop := max(len(items)-limit, 0)
	items = items[drop:]
	page := protocol.TextPage{SessionID: q.SessionID, Harness: "cto"}
	for i, it := range items {
		text, cut := cleanText(it.text, false)
		page.Entries = append(page.Entries, protocol.TextEntry{
			Cursor: strconv.Itoa(events.Older + drop + i),
			Time:   it.at / int64(time.Millisecond),
			Role:   it.role,
			Tool:   it.tool,
			Text:   text,
			Cut:    cut,
		})
	}
	if events.Older+drop > 0 {
		page.Older = strconv.Itoa(events.Older + drop)
	}
	if events.Next > 0 {
		page.Newer = strconv.Itoa(events.Next)
	}
	return page, nil
}

// ctoTextEntry turns one harness message into a timeline entry.
func ctoTextEntry(raw json.RawMessage) (role, text, tool string, at int64, ok bool) {
	var v struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Error   json.RawMessage `json:"error"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Time struct {
			Created int64 `json:"created"`
		} `json:"time"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return "", "", "", 0, false
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
		if json.Unmarshal(v.Error, &e) == nil {
			text = firstNonEmpty(firstNonEmpty(e.Message, e.Data.Message), firstNonEmpty(e.Name, string(v.Error)))
		} else {
			text = string(v.Error)
		}
		return protocol.RoleAssistant, text, "cto_error", at, true
	}
	if v.Type != protocol.RoleUser && v.Type != protocol.RoleAssistant {
		return "", "", "", 0, false
	}
	text = v.Text
	for _, p := range v.Content {
		if p.Type == "text" {
			text += p.Text
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", "", "", 0, false
	}
	return v.Type, text, "", at, true
}

// ctoOnThisNode tells whether this daemon holds the registered CTO session. A
// machine that hosted it before the owner moved it keeps the old local ID, and
// must relay instead of starting a second CTO. When the gateway cannot answer,
// the local record is the best information there is.
func (d *Daemon) ctoOnThisNode(ctx context.Context) bool {
	data, err := d.call(ctx, "query.agents", protocol.QueryAgents{IncludeOwners: true, Harness: "cto"})
	if err != nil {
		return true
	}
	var r protocol.AgentsResult
	if json.Unmarshal(data, &r) != nil {
		return true
	}
	for _, a := range r.Agents {
		if a.Harness == "cto" {
			return a.NodeID == d.cfg.NodeID
		}
	}
	return true
}

// ctoTextQuery answers session_text for the CTO session. handled is false when
// the query is for another session.
func (d *Daemon) ctoTextQuery(ctx context.Context, q protocol.TextQuery) (bool, protocol.TextPage, error) {
	id := d.ctoSessionID()
	if q.SessionID == protocol.CTONodeName {
		if id == "" {
			return true, protocol.TextPage{}, errors.New("the CTO session is not registered; retry after the gateway reconnects")
		}
		q.SessionID = id
	}
	if id == "" || q.SessionID != id {
		return false, protocol.TextPage{}, nil
	}
	if !d.ctoOnThisNode(ctx) {
		return false, protocol.TextPage{}, nil
	}
	page, err := d.ctoTextPage(ctx, q)
	if err != nil {
		return true, protocol.TextPage{}, err
	}
	page.SessionID, page.Harness = id, "cto"
	return true, page, nil
}

// ctoEnterQuery answers session_enter for the CTO session. handled is false
// when the query is for another session.
func (d *Daemon) ctoEnterQuery(ctx context.Context, q protocol.EnterRequest, viaGateway bool) (bool, protocol.EnterPlan, error) {
	id := d.ctoSessionID()
	if q.SessionID == protocol.CTONodeName {
		if id == "" {
			return true, protocol.EnterPlan{}, errors.New("the CTO session is not registered; retry after the gateway reconnects")
		}
		q.SessionID = id
	}
	if id == "" || q.SessionID != id {
		return false, protocol.EnterPlan{}, nil
	}
	if !d.ctoOnThisNode(ctx) {
		return false, protocol.EnterPlan{}, nil
	}
	plan, err := d.ctoEnter(ctx)
	if err == nil {
		plan = d.viewPlan("cto:"+strings.Join(plan.Argv, "\x00"), "CTO", viaGateway && q.HerdrClient, plan)
	}
	if viaGateway && err == nil {
		if plan.OpenedHerdr && q.HerdrClient {
			plan.Argv = herdrSessionAttach(plan.HerdrSession, plan.HerdrPane)
			plan.OpenedHerdr = false
		} else if len(plan.Env) > 0 {
			// OpenCode's private server password must stay on this machine.
			if plan.Env["OPENCODE_SERVER_PASSWORD"] != "" {
				return true, protocol.EnterPlan{Note: "The CTO's private server can only be entered on its own machine."}, nil
			}
			plan.Env = nil
		}
		plan.Name = "CTO"
	}
	return true, plan, err
}
