package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// claudeSender says who sent a Claude Code prompt, as Claude Code records it
// in the transcript entry of that prompt.
type claudeSender int

const (
	// senderUnknown: the entry names no origin. The owner's presence applies,
	// as it did before this check, but his words are not kept: nothing proves
	// a human typed the prompt.
	senderUnknown claudeSender = iota
	// senderOwner: the owner typed the prompt in the Claude Code terminal.
	senderOwner
	// senderOther: a peer message, a task notice, a scheduled or loop fire, an
	// SDK turn or another turn Claude Code writes itself.
	senderOther
)

const (
	// claudeSenderTail is how much of the end of a transcript one read looks
	// at. A new prompt is written near the end, so the read stays small.
	claudeSenderTail = 256 << 10
	// claudeSenderPoll is the pause between reads while the entry is missing.
	claudeSenderPoll = 100 * time.Millisecond
	// claudeSenderWait is how long the daemon waits for Claude Code to write a
	// prompt. Past it the prompt gets the owner's effects, as before.
	claudeSenderWait = 5 * time.Second
)

// claudeTurn is the part of a transcript user entry that tells who sent it.
// Claude Code writes isMeta and turnOrigin on the entries of a prompt. The
// owner's typed or queued prompts have turnOrigin "human" and no isMeta.
type claudeTurn struct {
	Type       string `json:"type"`
	PromptID   string `json:"promptId"`
	IsMeta     bool   `json:"isMeta"`
	TurnOrigin string `json:"turnOrigin"`
}

// whenClaudeOwnerPrompt runs apply for a prompt that the daemon did not type
// and the harness did not inject. owner is true only when the transcript proves
// the owner typed the prompt. A caller may run its presence effects (closing
// waits, the last-use mark) also for owner false, but text it keeps or matches
// as the owner's words must run only for owner true.
//
// The prompt hook runs before Claude Code writes the prompt to the transcript,
// and the docs say the file may lag, so the entry is usually missing when the
// hook runs. The check then waits in the background. A prompt without a usable
// transcript is presence only, not words.
func (d *Daemon) whenClaudeOwnerPrompt(ctx context.Context, p protocol.PolicyRequest, apply func(context.Context, bool)) {
	// Claude Code records a line the daemon typed itself as a human prompt, so
	// the transcript cannot tell it from the owner. The daemon knows it.
	if d.daemonTyped.typed(p.NativeID, p.Prompt) {
		return
	}
	// A first message that agent-net or an agent passed on the command line
	// is recorded as typed too. A launch with the same text that has not
	// returned yet may still name this session, so the check then waits.
	skip, wait := d.nonOwnerFirstPrompt(p)
	if skip {
		return
	}
	if len(wait) == 0 {
		if !claudeTranscriptFor(p.TranscriptPath, p.NativeID, p.PromptID) {
			apply(ctx, false)
			return
		}
		if s, found := claudeSenderOf(p.TranscriptPath, p.NativeID, p.PromptID); found {
			if s != senderOther {
				apply(ctx, s == senderOwner)
			}
			return
		}
	}
	d.claudeChecks.Add(1)
	go func() {
		defer d.claudeChecks.Done()
		s := claudeSenderWithin(p.TranscriptPath, p.NativeID, p.PromptID, d.claudeWait())
		// The first message check runs again after each wait, before any
		// effect: a launch can bind its message to this session meanwhile.
		for {
			skip, wait := d.nonOwnerFirstPrompt(p)
			if skip {
				return
			}
			if len(wait) == 0 {
				break
			}
			for _, ch := range wait {
				<-ch
			}
		}
		if s != senderOther {
			apply(context.Background(), s == senderOwner)
		}
	}()
}

// claudeWait is how long the daemon waits for the transcript entry. Tests set
// claudeOwnerWait to keep a missing entry short.
func (d *Daemon) claudeWait() time.Duration {
	if d.claudeOwnerWait > 0 {
		return d.claudeOwnerWait
	}
	return claudeSenderWait
}

// pendingOwnerWords is a prompt whose transcript entry had not appeared when
// the daemon stopped waiting. The text is not the owner's words yet: it is
// read again later, and kept only if the entry then says a human typed it.
type pendingOwnerWords struct {
	session  string
	native   string
	path     string
	promptID string
	text     string
	// first is the first message hash of the prompt, checked again before
	// the words are kept.
	first string
	at    time.Time
}

const pendingWordsKeySep = "\x00"

// notePendingOwnerWords keeps a prompt to read again later. A loaded machine
// can write the transcript entry after the five second wait. The text is not
// kept as the owner's words until an entry says he typed it. Pasted and
// reminder blocks are removed before the text is cut, so a cut cannot expose
// text a block hid.
func (d *Daemon) notePendingOwnerWords(session string, p protocol.PolicyRequest) {
	if session == "" || p.PromptID == "" || !claudeTranscriptFor(p.TranscriptPath, p.NativeID, p.PromptID) {
		return
	}
	text, ok := ownerWordsText(p.Prompt)
	if !ok {
		return
	}
	now := d.ownerPromptNow()
	d.pendingWordsMu.Lock()
	defer d.pendingWordsMu.Unlock()
	if d.pendingWords == nil {
		d.pendingWords = map[string]pendingOwnerWords{}
	}
	count := 0
	oldestKey := ""
	var oldest time.Time
	for k, q := range d.pendingWords {
		if now.Sub(q.at) > ownerPromptWindow {
			delete(d.pendingWords, k)
			continue
		}
		if q.session != session {
			continue
		}
		count++
		if oldestKey == "" || q.at.Before(oldest) {
			oldestKey, oldest = k, q.at
		}
	}
	if count >= maxOwnerPromptsPerSession && oldestKey != "" {
		delete(d.pendingWords, oldestKey)
	}
	d.pendingWords[session+pendingWordsKeySep+p.PromptID] = pendingOwnerWords{
		session: session, native: p.NativeID, path: p.TranscriptPath,
		promptID: p.PromptID, text: text, first: firstMessageHash(p.Prompt), at: now,
	}
}

// resolvePendingOwnerWords reads the transcript again for prompts of the
// session whose entry was missing when the daemon stopped waiting. It keeps the
// words of an entry that now says the owner typed them and drops an entry that
// says someone else did. A prompt whose entry is still missing stays for the
// next call, until its window ends. There is no poll: this runs when the words
// are needed and at the end of a turn.
func (d *Daemon) resolvePendingOwnerWords(session string) {
	if session == "" {
		return
	}
	prefix := session + pendingWordsKeySep
	now := d.ownerPromptNow()
	d.pendingWordsMu.Lock()
	if len(d.pendingWords) == 0 {
		d.pendingWordsMu.Unlock()
		return
	}
	list := make([]pendingOwnerWords, 0, len(d.pendingWords))
	for k, q := range d.pendingWords {
		if strings.HasPrefix(k, prefix) {
			list = append(list, q)
		}
	}
	d.pendingWordsMu.Unlock()
	for _, q := range list {
		key := q.session + pendingWordsKeySep + q.promptID
		if now.Sub(q.at) > ownerPromptWindow {
			d.pendingWordsMu.Lock()
			delete(d.pendingWords, key)
			d.pendingWordsMu.Unlock()
			continue
		}
		s, found := claudeSenderOf(q.path, q.native, q.promptID)
		if !found {
			continue
		}
		// A first message that a launch passed is never words, however
		// late its entry. A launch that still runs decides at a later read.
		first, wait := d.firstMessageMatch(q.native, q.promptID, q.first)
		if !first && len(wait) > 0 {
			continue
		}
		d.pendingWordsMu.Lock()
		_, still := d.pendingWords[key]
		if still {
			delete(d.pendingWords, key)
		}
		d.pendingWordsMu.Unlock()
		if still && !first && s == senderOwner {
			d.rememberOwnerPromptAt(session, q.text, q.at)
		}
	}
}

// claudeTranscriptFor says whether path is the transcript of the session. Claude
// Code names a transcript after its session, so a path with another name is
// not read.
func claudeTranscriptFor(path, sessionID, promptID string) bool {
	return promptID != "" && path != "" && sessionID != "" && filepath.Base(path) == sessionID+".jsonl"
}

// claudeSenderOf reads the entry of one prompt from the end of its transcript.
// found is false while the entry is not in that tail yet.
func claudeSenderOf(path, sessionID, promptID string) (claudeSender, bool) {
	if !claudeTranscriptFor(path, sessionID, promptID) {
		return senderUnknown, false
	}
	buf, err := readTranscriptTail(path, claudeSenderTail)
	if err != nil {
		return senderUnknown, false
	}
	needle := []byte(promptID)
	lines := bytes.Split(buf, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], needle) {
			continue
		}
		// The first line of a tail may be cut off. It does not parse, so it
		// is skipped like any other line that is not the entry.
		var e claudeTurn
		if json.Unmarshal(lines[i], &e) != nil || e.Type != "user" || e.PromptID != promptID {
			continue
		}
		return classifyClaudeTurn(e), true
	}
	return senderUnknown, false
}

// claudeSenderWithin waits up to wait for the entry of a prompt and returns
// who sent it. senderUnknown means the entry never appeared in time.
func claudeSenderWithin(path, sessionID, promptID string, wait time.Duration) claudeSender {
	if !claudeTranscriptFor(path, sessionID, promptID) {
		return senderUnknown
	}
	deadline := time.Now().Add(wait)
	for {
		if s, found := claudeSenderOf(path, sessionID, promptID); found {
			return s
		}
		if !time.Now().Before(deadline) {
			return senderUnknown
		}
		time.Sleep(claudeSenderPoll)
	}
}

// classifyClaudeTurn maps the origin fields of an entry to a sender. A turn
// Claude Code marks as meta, or whose turnOrigin is not the human's, was not
// typed by the owner. An entry with neither field gives no answer.
func classifyClaudeTurn(e claudeTurn) claudeSender {
	switch {
	case e.IsMeta:
		return senderOther
	case e.TurnOrigin == "human":
		return senderOwner
	case e.TurnOrigin == "":
		return senderUnknown
	default:
		return senderOther
	}
}

// readTranscriptTail returns the last n bytes of a file, or the whole file when
// it is shorter.
func readTranscriptTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := max(fi.Size()-n, 0)
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil, err
	}
	return buf, nil
}
