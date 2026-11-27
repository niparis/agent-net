package daemon

import (
	"context"
	"encoding/json"
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
	ownerPromptWindow = protocol.OwnerWordsWindow
	// maxOwnerPromptsPerSession and maxOwnerPromptBytes bound the memory used.
	maxOwnerPromptsPerSession = 20
	maxOwnerPromptBytes       = 16 * 1024
)

// ownerPrompts keeps what the owner typed in each session, for the last few
// hours. It lives in memory only and is never logged: the daemon learns the
// text from the Claude Code prompt hook (policyCall), and after a restart from
// the Claude transcripts (sweepQueuedOwnerPrompts). A harness without those has
// none, so its starts go through the Desk ask.
type ownerPrompts struct {
	mu sync.Mutex
	by map[string][]ownerPrompt
	// sweepMu serialises the transcript sweeps and guards scanned, the byte
	// offset each transcript was read up to, and stopQueued
	// (queued_owner_prompts.go).
	sweepMu sync.Mutex
	scanned map[string]int64
	// stopQueued is the last queued line of each session that a sweep read
	// after the first read of its transcript and that the Stop hook has not
	// taken yet. Any sweep may read a line first (the link-up refill, a quote
	// check), so the Stop hook cannot rely on its own sweep to find it.
	stopQueued map[string]queuedLine
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
	d.rememberOwnerPromptAt(session, text, d.ownerPromptNow())
}

// ownerWordsText returns the prompt cut down to what may be kept as the
// owner's words: pasted blocks and system reminders removed, the parts he typed
// joined with OwnerTypedSeparator, and the result cut to the store's limit.
// Blocks are removed before the cut, so a cut can never turn text that a block
// hid into a quote. ok is false when he typed nothing outside a block.
func ownerWordsText(text string) (string, bool) {
	segments := protocol.OwnerTypedSegments(text)
	if len(segments) == 0 {
		return "", false
	}
	text = strings.Join(segments, protocol.OwnerTypedSeparator)
	if len(text) > maxOwnerPromptBytes {
		text = strings.ToValidUTF8(text[:maxOwnerPromptBytes], "")
	}
	return text, true
}

// rememberOwnerPromptAt is rememberOwnerPrompt for a prompt typed at a known
// time, such as one read back from a transcript. Text the harness marks as
// pasted is removed here, so every store of the owner's words holds only what
// he typed.
func (d *Daemon) rememberOwnerPromptAt(session, text string, at time.Time) {
	if session == "" {
		return
	}
	text, ok := ownerWordsText(text)
	if !ok {
		return
	}
	now := d.ownerPromptNow()
	if now.Sub(at) > ownerPromptWindow {
		return
	}
	d.keepOwnerPrompt(session, text, at, now)
	// The gateway keeps the words too, so a quote counts on every machine.
	// The call runs outside every lock: this runs under the sweep's lock. A
	// daemon that has not started has no gateway link.
	if d.ctoCtx == nil {
		return
	}
	d.goBackground(func() {
		ctx, cancel := context.WithTimeout(d.ctoCtx, 30*time.Second)
		defer cancel()
		if _, err := d.call(ctx, protocol.FrameOwnerWordsKeep, protocol.OwnerWordsKeep{Session: session, Text: text, At: at.UnixNano()}); err != nil && !strings.Contains(err.Error(), "unknown frame type") {
			d.Logf("owner words: not kept at the gateway: %v", err)
		}
	})
}

func (d *Daemon) keepOwnerPrompt(session, text string, at, now time.Time) {
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
	list := d.ownerPrompts.by[session]
	// The hook and a transcript read can both keep one prompt: it is kept
	// once, at its latest time.
	for i, p := range list {
		if p.text == text {
			if at.After(p.at) {
				list[i].at = at
			}
			return
		}
	}
	list = append(list, ownerPrompt{at: at, text: text})
	if len(list) > maxOwnerPromptsPerSession {
		list = list[len(list)-maxOwnerPromptsPerSession:]
	}
	d.ownerPrompts.by[session] = list
}

// refillOwnerWords sends every prompt this machine keeps to the gateway. A
// gateway keeps the words in memory only, so after it restarts it has them
// again from the machines that reconnect. The transcripts are read first, so a
// daemon that restarted too sends what the owner typed before.
func (d *Daemon) refillOwnerWords(ctx context.Context) {
	d.sweepAllOwnerPrompts()
	now := d.ownerPromptNow()
	var keep []protocol.OwnerWordsKeep
	d.ownerPrompts.mu.Lock()
	for session, list := range d.ownerPrompts.by {
		for _, p := range freshPrompts(list, now) {
			keep = append(keep, protocol.OwnerWordsKeep{Session: session, Text: p.text, At: p.at.UnixNano()})
		}
	}
	d.ownerPrompts.mu.Unlock()
	for _, k := range keep {
		if _, err := d.call(ctx, protocol.FrameOwnerWordsKeep, k); err != nil {
			if !strings.Contains(err.Error(), "unknown frame type") {
				d.Logf("owner words: not sent to the gateway again: %v", err)
			}
			return
		}
	}
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
	if session == "" {
		return false
	}
	_, ok := d.localOwnerWords(quote, session)
	return ok
}

// ownerWordsSource is the session and the time of the prompt that holds a
// quote of the owner.
type ownerWordsSource struct {
	session string
	at      time.Time
}

// localOwnerWords finds the quote in the prompts this machine keeps: in the
// session when one is named, else in every session. It returns the latest
// prompt that holds it.
func (d *Daemon) localOwnerWords(quote, session string) (ownerWordsSource, bool) {
	if session != "" {
		// A prompt whose transcript entry was missing when the wait ended
		// may have one now. Read it again before matching.
		d.resolvePendingOwnerWords(session)
		// Text typed while a Claude turn runs fires no prompt hook: read it
		// from the transcript now, when it is needed.
		d.sweepQueuedOwnerPrompts(session)
	} else {
		d.sweepAllOwnerPrompts()
	}
	now := d.ownerPromptNow()
	d.ownerPrompts.mu.Lock()
	defer d.ownerPrompts.mu.Unlock()
	var best ownerWordsSource
	found := false
	for id, list := range d.ownerPrompts.by {
		if session != "" && id != session {
			continue
		}
		for _, p := range freshPrompts(list, now) {
			if (!found || p.at.After(best.at)) && protocol.OwnerWordsMatch(quote, []string{p.text}) {
				best, found = ownerWordsSource{session: id, at: p.at}, true
			}
		}
	}
	return best, found
}

// findOwnerWords finds the quote in what the owner typed in the last
// ownerPromptWindow: first in the caller's session, then in any session of
// this machine, then in the gateway's store of every machine.
func (d *Daemon) findOwnerWords(ctx context.Context, caller, quote string) (ownerWordsSource, bool) {
	if src, ok := d.localOwnerWords(quote, caller); ok {
		return src, true
	}
	if src, ok := d.localOwnerWords(quote, ""); ok {
		return src, true
	}
	data, err := d.call(ctx, protocol.FrameOwnerWordsFind, protocol.OwnerWordsFind{Quote: quote})
	if err != nil {
		if !strings.Contains(err.Error(), "unknown frame type") {
			d.Logf("owner words: the gateway could not look up the quote: %v", err)
		}
		return ownerWordsSource{}, false
	}
	var found protocol.OwnerWordsFound
	if json.Unmarshal(data, &found) != nil || !found.Found || found.Session == "" {
		return ownerWordsSource{}, false
	}
	at := time.Unix(0, found.At)
	if d.ownerPromptNow().Sub(at) > ownerPromptWindow {
		return ownerWordsSource{}, false
	}
	return ownerWordsSource{session: found.Session, at: at}, true
}

// checkOverride decides what a start with selection fields needs. It clears
// owner_words and reason from the request: they stop here. For a caller that
// sets a selection field it returns:
//   - selection SelectionOwnerWords when the quote is in the owner's own words,
//     with the receipt that names where he typed them: the start goes ahead;
//   - hold true with the caller's reason when the owner must approve it;
//   - no hold for the owner's own command line and TUI.
func (d *Daemon) checkOverride(ctx context.Context, caller string, p *protocol.WorkerStartRequest, callerIsAgent bool) (selection string, hold bool, reason, receipt string) {
	words, why := strings.TrimSpace(p.OwnerWords), strings.TrimSpace(p.Reason)
	p.OwnerWords, p.Reason = "", ""
	if !callerIsAgent || d.cfg.testOpenSelection {
		return "", false, "", ""
	}
	fields := p.SelectionFields()
	if len(fields) == 0 {
		return "", false, "", ""
	}
	if words != "" {
		if src, ok := d.findOwnerWords(ctx, caller, words); ok {
			d.Logf("worker.owner_words project=%s typed_in=%s", p.Project, src.session)
			return protocol.SelectionOwnerWords, false, "", protocol.OwnerWordsReceipt(d.sessionName(ctx, src.session), src.at)
		}
	}
	if why == "" {
		why = "the caller did not say why"
	}
	return "", true, why, ""
}

func fieldNames(fields []string) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i], _, _ = strings.Cut(f, "=")
	}
	return out
}
