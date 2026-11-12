package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// A caller cannot override agent-net's worker picker without a human
// confirming it. An agent's start that sets a selection field (harness, model,
// provider, effort, not_family, machine) goes ahead only with the owner's own
// words (owner_words, found in his prompts of that session) or after the owner
// confirms the override on the Desk. See protocol/worker_override.go.

const (
	// ownerPromptWindow is how long a prompt of the owner counts as his words.
	ownerPromptWindow = 3 * time.Hour
	// maxOwnerPromptsPerSession and maxOwnerPromptBytes bound the memory used.
	maxOwnerPromptsPerSession = 20
	maxOwnerPromptBytes       = 16 * 1024
)

// ownerPrompts keeps what the owner typed in each session, for the last few
// hours. It lives in memory only and is never logged: the daemon learns the
// text from the Claude Code prompt hook (policyCall). A harness without that
// hook has none, so its starts go through the Desk ask.
type ownerPrompts struct {
	mu sync.Mutex
	by map[string][]ownerPrompt
}

type ownerPrompt struct {
	at   time.Time
	text string
}

func (d *Daemon) ownerPromptNow() time.Time {
	if d.ownerPromptClock != nil {
		return d.ownerPromptClock()
	}
	return time.Now()
}

// rememberOwnerPrompt keeps a prompt the owner typed in a session. The caller
// has already dropped the turns a harness injects itself.
func (d *Daemon) rememberOwnerPrompt(session, text string) {
	text = strings.ToValidUTF8(strings.TrimSpace(text), "")
	if session == "" || text == "" {
		return
	}
	if len(text) > maxOwnerPromptBytes {
		text = strings.ToValidUTF8(text[:maxOwnerPromptBytes], "")
	}
	now := d.ownerPromptNow()
	d.ownerPrompts.mu.Lock()
	defer d.ownerPrompts.mu.Unlock()
	if d.ownerPrompts.by == nil {
		d.ownerPrompts.by = map[string][]ownerPrompt{}
	}
	for id, list := range d.ownerPrompts.by {
		if fresh := freshPrompts(list, now); len(fresh) == 0 {
			delete(d.ownerPrompts.by, id)
		} else {
			d.ownerPrompts.by[id] = fresh
		}
	}
	list := append(d.ownerPrompts.by[session], ownerPrompt{at: now, text: text})
	if len(list) > maxOwnerPromptsPerSession {
		list = list[len(list)-maxOwnerPromptsPerSession:]
	}
	d.ownerPrompts.by[session] = list
}

func freshPrompts(list []ownerPrompt, now time.Time) []ownerPrompt {
	var out []ownerPrompt
	for _, p := range list {
		if now.Sub(p.at) <= ownerPromptWindow {
			out = append(out, p)
		}
	}
	return out
}

// ownerWordsMatch tells whether the quote is in what the owner typed in the
// session in the last ownerPromptWindow, ignoring case and white space.
func (d *Daemon) ownerWordsMatch(session, quote string) bool {
	now := d.ownerPromptNow()
	d.ownerPrompts.mu.Lock()
	var texts []string
	for _, p := range freshPrompts(d.ownerPrompts.by[session], now) {
		texts = append(texts, p.text)
	}
	d.ownerPrompts.mu.Unlock()
	return protocol.OwnerWordsMatch(quote, texts)
}

// checkOverride decides what an agent's start with selection fields needs.
// It clears owner_words and reason from the request: they stop here. For an
// agent whose start sets a selection field it returns:
//   - selection SelectionOwnerWords when the quote matches: the start goes ahead;
//   - hold true with the caller's reason when the start must wait for the owner;
//   - an error when there is no match and no reason, which says what to give.
//
// A start from the owner's command line or the CTO, and a start with no
// selection field, is not an override.
func (d *Daemon) checkOverride(caller string, p *protocol.WorkerStartRequest, agent bool) (selection string, hold bool, reason string, err error) {
	words, why := strings.TrimSpace(p.OwnerWords), strings.TrimSpace(p.Reason)
	p.OwnerWords, p.Reason = "", ""
	if !agent || d.cfg.testOpenSelection {
		return "", false, "", nil
	}
	fields := p.SelectionFields()
	if len(fields) == 0 {
		return "", false, "", nil
	}
	if words != "" && d.ownerWordsMatch(caller, words) {
		return protocol.SelectionOwnerWords, false, "", nil
	}
	if why == "" {
		if words != "" {
			return "", false, "", fmt.Errorf("owner_words %q was not found in what your owner typed in this session in the last %d hours, so agent-net cannot treat it as his request. Leave %s empty and let the picker choose, or give a reason: the start then waits for your owner to confirm it", words, int(ownerPromptWindow.Hours()), strings.Join(fieldNames(fields), ", "))
		}
		return "", false, "", fmt.Errorf("%s set without owner_words: a caller cannot override the picker without a human. Leave them empty, or pass owner_words with a quote of your owner's request, or give a reason: the start then waits for your owner to confirm it", strings.Join(fieldNames(fields), ", "))
	}
	return "", true, why, nil
}

func fieldNames(fields []string) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i], _, _ = strings.Cut(f, "=")
	}
	return out
}

// heldStart is a start that waits for the owner's answer on the Desk.
type heldStart struct {
	id            string
	caller        string
	req           protocol.WorkerStartRequest
	callerProject bool
	callerPath    string
}

// holdStart puts the ask on the Desk and answers the caller at once: the start
// is accepted and waits. The worker notice follows when it starts, or when it
// does not.
func (d *Daemon) holdStart(ctx context.Context, caller string, p protocol.WorkerStartRequest, reason string, callerProject bool, callerPath string) (*protocol.WorkerStartResult, error) {
	id := protocol.NewID("a")
	title := protocol.WorkerTitle(p.Title, p.BriefText(), "worker")
	text := protocol.StartOverrideText(d.sessionName(ctx, caller), p.SelectionFields(), title, reason)
	if _, err := d.call(ctx, "worker.override_ask", protocol.StartOverrideAsk{Session: caller, StartID: id, Text: text}); err != nil {
		if strings.Contains(err.Error(), "unknown frame type") {
			return nil, errors.New("the gateway is too old to hold a start for the owner: upgrade it with `agent-net upgrade` on the gateway machine, or leave the selection fields empty")
		}
		return nil, err
	}
	d.heldMu.Lock()
	if d.heldStarts == nil {
		d.heldStarts = map[string]*heldStart{}
	}
	d.heldStarts[id] = &heldStart{id: id, caller: caller, req: p, callerProject: callerProject, callerPath: callerPath}
	d.heldMu.Unlock()
	d.Logf("worker.held start=%s project=%s fields=%q", id, p.Project, strings.Join(fieldNames(p.SelectionFields()), ","))
	return &protocol.WorkerStartResult{Status: "accepted", StartID: id, Project: p.Project, Harness: p.Harness, Model: p.Model,
		SelectionPath: protocol.SelectionHeld,
		Note:          "the start is held: " + strings.Join(fieldNames(p.SelectionFields()), ", ") + " can only be chosen by your owner. An ask is on his Desk: confirm starts the worker with those fields, picker starts it with the picker's own choice. A worker notice arrives in your inbox when it starts or does not start. Do not start it again."}, nil
}

// onStartDecision is the owner's answer to the ask of a held start.
func (d *Daemon) onStartDecision(p protocol.StartDecision) {
	id := strings.TrimPrefix(p.Ref, protocol.StartOverrideRefPrefix)
	d.heldMu.Lock()
	h := d.heldStarts[id]
	delete(d.heldStarts, id)
	d.heldMu.Unlock()
	if h == nil {
		d.Logf("worker.held start=%s: answered %q but this daemon no longer holds it", id, p.Answer)
		d.startReport(d.ctoCtx, protocol.WorkerStartReport{ID: id, Owner: p.Session, Status: protocol.StartReportNotStarted,
			Note: "the machine lost the held start (it restarted while the owner's answer was pending): start it again"})
		return
	}
	req, lead := h.req, ""
	switch strings.TrimSpace(p.Answer) {
	case protocol.StartAnswerConfirm:
		lead = "The owner confirmed your choice of " + strings.Join(fieldNames(req.SelectionFields()), ", ") + ". "
	case protocol.StartAnswerPicker:
		req, lead = req.WithoutSelection(), "The owner let the picker choose. "
	default:
		d.Logf("worker.held start=%s: not started, owner answer %q", id, p.Answer)
		d.reportAcceptedStart(id, h.caller, req, nil, errors.New("your owner did not confirm the choice of "+strings.Join(fieldNames(req.SelectionFields()), ", ")+" (answer: "+oneLineAnswer(p.Answer)+"). Start again without selection fields, or ask him"))
		return
	}
	d.goBackground(func() {
		frame := protocol.WorkerStartFrame{WorkerStartRequest: req, Owner: h.caller, CallerProject: h.callerProject, CallerPath: h.callerPath}
		res, err := d.runWorkerStartCall(d.ctoCtx, h.caller, req, frame, h.callerPath, false, &callPhases{})
		if res != nil {
			res.OwnerLine = lead + res.OwnerLine
		}
		d.reportAcceptedStart(id, h.caller, req, res, err)
	})
}

func oneLineAnswer(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	if s == "dismiss" || s == "seen" {
		return "dismissed"
	}
	return s
}

// onStartDecisionFrame decodes the gateway's frame.
