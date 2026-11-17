package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A harness switch hands the CTO's open work to the new conversation (decision
// 0171). The handover is one gateway note, named handover-<UTC time>, with two
// layers. The mechanical layer comes from gateway reads and the daemon's own
// records, so it stands alone when the old harness is down. The model layer is
// the old conversation's own words, asked for within ctoHandoverModelWait, and
// is marked as model output.

// ctoHandoverModelWait bounds the whole model layer: the send and the answer.
const ctoHandoverModelWait = 60 * time.Second

// ctoHandoverPrompt asks the old conversation for words only. It must not start
// work, because the switch is already underway.
const ctoHandoverPrompt = "The owner is moving the CTO to another harness now. Write the handover for the next conversation in at most 200 words of plain text: the promises you made and what is still owed, and the threads that matter and why. Do not call any tool and do not start any work."

// ctoHandoverOwnerMessages is how many of the owner's messages the handover
// carries, verbatim.
const ctoHandoverOwnerMessages = 10

// ctoHandover writes the handover of a switch from one harness to another. The
// caller holds ctoDispatchMu and not cto.mu, and calls it before the old
// harness stops. ownerStarted is true for an owner switch. The model layer runs
// only for an owner switch from a harness that was running (answer), so a
// handover never starts a harness. A fallback writes the mechanical layer only.
func (d *Daemon) ctoHandover(ctx context.Context, from, to string, ownerStarted, answer bool) error {
	sid := d.ctoSessionID()
	if sid == "" {
		d.Logf("cto.handover skipped: the CTO network session is not registered")
		return nil
	}
	now := d.ctoNowTime()
	// Nanoseconds keep two handovers in one millisecond apart: a name is never
	// reused, so no handover is overwritten.
	name := "handover-" + now.UTC().Format("20060102-150405.000000000")
	busy, work := d.ctoLocalWork(ctx, from)
	if busy && ownerStarted {
		text := fmt.Sprintf("The CTO's %s turn was running when you switched to %s. It stopped. Its handover is note %s.", from, to, name)
		if _, err := d.call(ctx, "inbox.create", map[string]any{"session_id": sid, "kind": "check", "text": text}); err != nil {
			return fmt.Errorf("tell the owner about the running turn: %w", err)
		}
	}
	head := fmt.Sprintf("Handover from %s to %s at %s UTC.\n\n", from, to, now.UTC().Format("2006-01-02 15:04"))
	mechanical := head + d.ctoMechanicalHandover(ctx, sid, from, work)
	if err := d.writeHandoverNote(ctx, sid, name, from, to, now, mechanical); err != nil {
		return err
	}
	// A busy turn skips the model layer: the old conversation cannot answer
	// until its turn ends (decision 0171).
	if ownerStarted && answer && !busy {
		if answer, ok := d.ctoModelHandover(ctx, from); ok {
			body := mechanical + "\n\n## Model output (not the owner's words)\n\n" + answer
			if err := d.writeHandoverNote(ctx, sid, name, from, to, now, body); err != nil {
				return err
			}
		}
	}
	if _, err := d.st.db.Exec(`UPDATE cto SET handover_note=? WHERE id=1`, name); err != nil {
		return err
	}
	return nil
}

// writeHandoverNote stores the handover under its name. The same name again
// updates that note, so the second write adds the model layer to the first.
func (d *Daemon) writeHandoverNote(ctx context.Context, sid, name, from, to string, at time.Time, body string) error {
	req := protocol.NoteWriteFrame{NoteWriteRequest: protocol.NoteWriteRequest{
		Name:        name,
		Description: fmt.Sprintf("Handover from %s to %s at %s UTC", from, to, at.UTC().Format("2006-01-02 15:04")),
		Body:        body,
		Scope:       protocol.NoteScopeAgent,
		Kind:        protocol.StandingKindFact,
	}, Session: sid}
	if _, err := d.call(ctx, "note.write", req); err != nil {
		return fmt.Errorf("write handover note %s: %w", name, oldGateway(err))
	}
	return nil
}

// latestHandoverNote is the name of the newest handover, or "".
func (d *Daemon) latestHandoverNote() string {
	if d.st == nil || d.st.db == nil {
		return ""
	}
	var name string
	if err := d.st.db.QueryRow(`SELECT handover_note FROM cto WHERE id=1`).Scan(&name); err != nil {
		return ""
	}
	return name
}

// ctoMechanicalHandover is the layer agent-net writes without a model. Each
// gateway read that fails is written as a line, so one failure never drops the
// whole note.
func (d *Daemon) ctoMechanicalHandover(ctx context.Context, sid, from, work string) string {
	var b strings.Builder
	asks, owed, err := d.handoverAsks(ctx, sid)
	writeHandoverSection(&b, "Open asks the CTO raised", asks, err)
	writeHandoverSection(&b, "Messages the CTO owes a reply", owed, nil)

	workers, err := d.handoverWorkers(ctx, sid)
	writeHandoverSection(&b, "Workers the CTO started that are not done", workers, err)

	changes, err := d.handoverChanges(ctx, sid)
	writeHandoverSection(&b, "Changes the CTO started that are not done", changes, err)

	fmt.Fprintf(&b, "Harness-local work (%s): %s. agent-net sees a busy OpenCode turn and the Claude job state only; subagents, background shells and monitors are not visible to it.\n\n", from, work)

	b.WriteString("Owner's last messages, verbatim, oldest first:\n")
	msgs, err := d.ctoOwnerMessages(ctoHandoverOwnerMessages)
	if err != nil {
		fmt.Fprintf(&b, "- could not read them: %v\n", err)
	}
	for _, m := range msgs {
		fmt.Fprintf(&b, "- %s UTC:\n%s\n", time.Unix(0, m.at).UTC().Format("2006-01-02 15:04"), m.text)
	}
	if len(msgs) == 0 && err == nil {
		b.WriteString("- none recorded\n")
	}
	return b.String()
}

// writeHandoverSection writes one titled list. A read error takes the place of
// the list.
func writeHandoverSection(b *strings.Builder, title string, lines []string, err error) {
	fmt.Fprintf(b, "%s (%d):\n", title, len(lines))
	if err != nil {
		fmt.Fprintf(b, "- could not read it: %v\n", err)
	}
	for _, line := range lines {
		fmt.Fprintf(b, "- %s\n", line)
	}
	b.WriteString("\n")
}

// handoverAsks reads the open asks: the ones the CTO raised, and the ones that
// name the CTO as the one who must act now.
func (d *Daemon) handoverAsks(ctx context.Context, sid string) (raised, owed []string, err error) {
	raw, err := d.call(ctx, "asks.list", struct{}{})
	if err != nil {
		return nil, nil, err
	}
	var res protocol.AsksResult
	if err = json.Unmarshal(raw, &res); err != nil {
		return nil, nil, err
	}
	for _, ask := range res.Asks {
		line := fmt.Sprintf("%s (%s): %s", ask.ID, ask.Kind, firstLineOf(ask.Question))
		if sid != "" && ask.Next == sid {
			owed = append(owed, line)
		} else if sid != "" && ask.FromSession == sid {
			raised = append(raised, line)
		}
	}
	return raised, owed, nil
}

// handoverWorkers lists the workers the CTO started that are still online.
func (d *Daemon) handoverWorkers(ctx context.Context, sid string) ([]string, error) {
	res, err := d.workersView(ctx, protocol.WorkersQuery{})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, w := range res.Workers {
		if w.Owner == sid && w.Online {
			out = append(out, fmt.Sprintf("%s %s (%s, %s)", w.SessionID, firstLineOf(w.Title), w.Project, w.Status))
		}
	}
	return out, nil
}

// handoverChanges lists the changes the CTO submitted that are open or merged.
func (d *Daemon) handoverChanges(ctx context.Context, sid string) ([]string, error) {
	res, err := d.changesFind(ctx, sid, protocol.ChangesFindArgs{Caller: protocol.ChangesCallerMe})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range res.Items {
		out = append(out, fmt.Sprintf("%s %s (%s, %s)", v.Change.ID, firstLineOf(v.Change.Title), v.Change.Project, v.Change.State))
	}
	return out, nil
}

// ctoOwnerMsg is one message the owner sent to the CTO.
type ctoOwnerMsg struct {
	at   int64
	text string
}

// ctoOwnerMessages reads the last n messages the owner sent to the CTO, oldest
// first. The daemon records them when it delivers them, so they do not depend on
// a harness.
func (d *Daemon) ctoOwnerMessages(n int) ([]ctoOwnerMsg, error) {
	rows, err := d.st.db.Query(`SELECT at,text FROM cto_owner_messages ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ctoOwnerMsg
	for rows.Next() {
		var m ctoOwnerMsg
		if err = rows.Scan(&m.at, &m.text); err != nil {
			return nil, err
		}
		out = append([]ctoOwnerMsg{m}, out...)
	}
	return out, rows.Err()
}

// recordCTOOwnerMessage keeps what the owner typed to the CTO, for the next
// handover.
func (d *Daemon) recordCTOOwnerMessage(text string) error {
	_, err := d.st.db.Exec(`INSERT INTO cto_owner_messages(at,text) VALUES(?,?)`, d.ctoNowTime().UnixNano(), text)
	return err
}

// ctoHarnessRunning says whether the CTO's harness has a live process or job.
func (d *Daemon) ctoHarnessRunning(harness string) bool {
	if harness == "claude" {
		d.cto.mu.Lock()
		defer d.cto.mu.Unlock()
		return d.claudeCTO.ref != ""
	}
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	return d.cto.base != "" && d.cto.session != ""
}

// ctoLocalWork says whether the old harness is in a busy turn, and what the
// daemon can see of it. An unknown answer is not busy.
func (d *Daemon) ctoLocalWork(ctx context.Context, harness string) (bool, string) {
	if harness == "claude" {
		d.cto.mu.Lock()
		ref := d.claudeCTO.ref
		d.cto.mu.Unlock()
		if ref == "" {
			return false, "no Claude job running"
		}
		status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, ref)
		if status == "" {
			status = "unknown"
		}
		return status == "running", "Claude job state " + status
	}
	c := &d.cto
	c.mu.Lock()
	base, password, version, identity, session := c.base, c.password, c.version, c.identity, c.session
	c.mu.Unlock()
	if base == "" || session == "" {
		return false, "no OpenCode server running"
	}
	busy, known := d.openCodeSessionBusy(ctx, base, password, version, identity, session)
	switch {
	case !known:
		return false, "OpenCode turn state unknown"
	case busy:
		return true, "busy turn in OpenCode"
	}
	return false, "no busy turn in OpenCode"
}

// ctoModelHandover asks the running old conversation for its handover and waits
// up to ctoHandoverModelWait for the answer. Any failure, timeout or busy
// turn that does not end gives ok false, and the mechanical layer stands alone.
func (d *Daemon) ctoModelHandover(ctx context.Context, from string) (string, bool) {
	var h ctoHarness = openCodeCTOHarness{}
	if from == "claude" {
		h = claudeCTOHarness{}
	}
	limit := ctoHandoverModelWait
	if d.cfg.testHandoverWait > 0 {
		limit = d.cfg.testHandoverWait
	}
	wait, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	before, err := h.events(wait, d, 0)
	if err != nil {
		return "", false
	}
	start := before.Next
	if _, err = h.send(wait, d, ctoHandoverPrompt); err != nil {
		return "", false
	}
	// Each poll reads the whole conversation, so the interval is a second.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-wait.Done():
			return "", false
		case <-ticker.C:
		}
		events, err := h.events(wait, d, start)
		if err != nil {
			continue
		}
		answer := ""
		for _, raw := range events.Messages {
			if text, ok := ctoAssistantText(raw); ok {
				answer = text
			}
		}
		if answer == "" {
			continue
		}
		if busy, _ := d.ctoLocalWork(wait, from); busy {
			continue
		}
		return answer, true
	}
}

// ctoAssistantText reads the text of one assistant message. It takes the
// transcript form (text) and the OpenCode form (content parts).
func ctoAssistantText(raw json.RawMessage) (string, bool) {
	var m struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Info struct {
			Role string `json:"role"`
		} `json:"info"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return "", false
	}
	if m.Type != "assistant" && m.Info.Role != "assistant" {
		return "", false
	}
	text := m.Text
	for _, part := range m.Content {
		if part.Type == "text" {
			text += part.Text
		}
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

// firstLineOf is the first line of a text, trimmed, for one line of a note.
func firstLineOf(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(line)
}

// dropHandoverExchange removes the handover request the daemon sent to the old
// conversation, and the reply to it, from what the owner sees. The conversation
// keeps them, since it is the same conversation when the harness comes back, but
// the owner did not write them (decision 0171). The model layer reads the raw
// conversation and does not use this.
func dropHandoverExchange(messages []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(messages))
	inExchange := false
	for _, raw := range messages {
		var m struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Info struct {
				Role string `json:"role"`
			} `json:"info"`
		}
		if json.Unmarshal(raw, &m) != nil {
			out = append(out, raw)
			continue
		}
		if m.Type == "user" {
			inExchange = strings.TrimSpace(m.Text) == ctoHandoverPrompt
			if !inExchange {
				out = append(out, raw)
			}
			continue
		}
		if inExchange && (m.Type == "assistant" || m.Info.Role == "assistant") {
			continue
		}
		out = append(out, raw)
	}
	return out
}
