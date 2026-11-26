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
)

// Text the owner types in a Claude Code pane while a turn runs does not fire
// the prompt hook. Claude Code queues it and hands it to the running turn, and
// the transcript records it as an attachment of type queued_command. The
// daemon reads those attachments back, so what the owner typed counts as his
// words whether or not the session was busy.
//
// The read happens when the words are needed (ownerWordsMatch) and at the end
// of a turn (the Stop hook). Nothing polls. Each transcript is read from the
// byte where the last sweep stopped, so a sweep costs what was appended since.

// queuedFirstRead is the step back of the first read of a transcript
// (transcriptWindowStart).
const queuedFirstRead = 2 << 20

// queuedAttachment is the part of a transcript line that tells what a queued
// command is and who sent it.
type queuedAttachment struct {
	Type       string `json:"type"`
	Timestamp  string `json:"timestamp"`
	Attachment struct {
		Type        string          `json:"type"`
		Prompt      json.RawMessage `json:"prompt"`
		CommandMode string          `json:"commandMode"`
		Origin      *struct {
			Kind string `json:"kind"`
		} `json:"origin"`
	} `json:"attachment"`
}

var queuedMark = []byte(`"queued_command"`)

// queuedLine is one queued command of a transcript. owner says whether the
// command names a human origin, which is what makes its text the owner's words.
// A command with no origin is unknown: it shows he was there, as before, but
// nothing proves he typed it.
type queuedLine struct {
	text  string
	owner bool
	at    time.Time
}

// queuedOwnerText returns what the owner typed when the line is a queued
// command he sent. ok is true for a plain prompt that is not injected; owner is
// true only when the command names a human origin. A command Claude Code or
// agent-net sent (a task notice, a peer message, a scheduled turn, an injected
// reminder) has an origin that is not the human's, or a mode that is not a
// plain prompt, and gives nothing.
func queuedOwnerText(line []byte) (text string, at time.Time, owner bool, ok bool) {
	if !bytes.Contains(line, queuedMark) {
		return "", time.Time{}, false, false
	}
	var e queuedAttachment
	if json.Unmarshal(line, &e) != nil || e.Type != "attachment" || e.Attachment.Type != "queued_command" {
		return "", time.Time{}, false, false
	}
	if o := e.Attachment.Origin; o != nil && o.Kind != "" && o.Kind != "human" {
		return "", time.Time{}, false, false
	}
	owner = e.Attachment.Origin != nil && e.Attachment.Origin.Kind == "human"
	if m := e.Attachment.CommandMode; m != "" && m != "prompt" {
		return "", time.Time{}, false, false
	}
	text = strings.TrimSpace(queuedPromptText(e.Attachment.Prompt))
	if text == "" || !isHumanClaudePrompt(text) {
		return "", time.Time{}, false, false
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return "", time.Time{}, false, false
	}
	return text, at, owner, true
}

// queuedPromptText reads a prompt that is a string or a list of text blocks.
func queuedPromptText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// claudeNativeOf names the Claude Code conversation behind a network session,
// or "" when it is not a Claude session of this daemon.
func (d *Daemon) claudeNativeOf(session string) string {
	if session == "" {
		return ""
	}
	if session == d.ctoSessionID() {
		return d.claudeCTO.sessionID()
	}
	if ls := d.sessionByID(session); ls != nil {
		if info := d.infoOf(ls); info.Harness == "claude" {
			return info.NativeID
		}
	}
	return ""
}

// sweepQueuedOwnerPrompts keeps the owner's queued prompts of a session that
// were written to its transcript since the last sweep and returns the new ones,
// oldest first. Only a command that names a human origin is kept as his words;
// a command with no origin is returned for presence alone. primed says whether
// an earlier sweep had already read this transcript (the first read finds old
// text, which is not news).
//
// The first read of a transcript in this daemon's life also keeps the prompts
// the owner typed in the last ownerPromptWindow (rebuildTypedPrompts). The
// prompt hook keeps them while the daemon runs, but they live in memory, so
// after a restart this read is where his words come from again. Nothing is
// written to disk: the transcript is Claude Code's own file.
func (d *Daemon) sweepQueuedOwnerPrompts(session string) (fresh []queuedLine, primed bool) {
	native := d.claudeNativeOf(session)
	if native == "" || strings.ContainsAny(native, `/\`) {
		return nil, false
	}
	projects := filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects")
	matches, _ := filepath.Glob(filepath.Join(projects, "*", native+".jsonl"))
	if len(matches) == 0 {
		return nil, false
	}
	path := matches[0]
	now := d.ownerPromptNow()
	var typed []typedTurn
	d.ownerPrompts.sweepMu.Lock()
	off, primed := d.ownerPrompts.scanned[path]
	buf, end, err := readTranscriptFrom(path, off, primed, now.Add(-ownerPromptWindow))
	if err != nil {
		d.ownerPrompts.sweepMu.Unlock()
		return nil, primed
	}
	if d.ownerPrompts.scanned == nil {
		d.ownerPrompts.scanned = map[string]int64{}
	}
	d.ownerPrompts.scanned[path] = end
	for _, line := range bytes.Split(buf, []byte{'\n'}) {
		if text, at, owner, ok := queuedOwnerText(line); ok && now.Sub(at) <= ownerPromptWindow {
			fresh = append(fresh, queuedLine{text: text, owner: owner, at: at})
			continue
		}
		if !primed {
			if t, ok := typedUserTurn(line); ok && now.Sub(t.at) <= ownerPromptWindow {
				typed = append(typed, t)
			}
		}
	}
	// The first sweep of a transcript waits for the next one, so the
	// transcript is read once. What is kept is kept outside the lock: the
	// first message check may write daemon.db.
	d.ownerPrompts.sweepMu.Unlock()
	d.rebuildTypedPrompts(session, native, typed)
	for _, q := range fresh {
		if q.owner {
			d.rememberOwnerPromptAt(session, q.text, q.at)
		}
	}
	return fresh, primed
}

// sweepAllOwnerPrompts sweeps the transcript of every Claude session of this
// machine, the CTO's too. A quote looked up in any session needs them all.
func (d *Daemon) sweepAllOwnerPrompts() {
	var ids []string
	d.mu.Lock()
	for _, ls := range d.sessions {
		if ls.info.Harness == "claude" && ls.sessionID != "" {
			ids = append(ids, ls.sessionID)
		}
	}
	d.mu.Unlock()
	if cto := d.ctoSessionID(); cto != "" {
		ids = append(ids, cto)
	}
	for _, id := range ids {
		d.sweepQueuedOwnerPrompts(id)
	}
}

// typedTurn is a user entry of a transcript that the owner typed.
type typedTurn struct {
	promptID, text string
	at             time.Time
}

// claudeUserTurn is the part of a transcript user entry that tells who sent
// it, when, and what it says.
type claudeUserTurn struct {
	claudeTurn
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

var humanMark = []byte(`"human"`)

// typedUserTurn reads a user entry the owner typed: Claude Code marks it with
// turnOrigin "human" and no isMeta, as the prompt hook's check reads it. A
// turn Claude Code injects and a slash command it wraps give nothing.
func typedUserTurn(line []byte) (typedTurn, bool) {
	if !bytes.Contains(line, humanMark) {
		return typedTurn{}, false
	}
	var e claudeUserTurn
	if json.Unmarshal(line, &e) != nil || e.Type != "user" || e.PromptID == "" || classifyClaudeTurn(e.claudeTurn) != senderOwner {
		return typedTurn{}, false
	}
	text := strings.TrimSpace(queuedPromptText(e.Message.Content))
	if text == "" || !isHumanClaudePrompt(text) || strings.HasPrefix(text, "<command-") || strings.HasPrefix(text, "<local-command") {
		return typedTurn{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return typedTurn{}, false
	}
	return typedTurn{promptID: e.PromptID, text: text, at: at}, true
}

// rebuildTypedPrompts keeps the prompts the owner typed, read back from the
// transcript. A first message that agent-net or an agent passed on the
// command line is recorded as typed too; its mark in daemon.db says it is not
// his, as it does for the prompt hook.
func (d *Daemon) rebuildTypedPrompts(session, native string, typed []typedTurn) {
	for _, t := range typed {
		if first, wait := d.firstMessageMatch(native, t.promptID, firstMessageHash(t.text)); first || len(wait) > 0 {
			continue
		}
		d.rememberOwnerPromptAt(session, t.text, t.at)
	}
}

// readTranscriptFrom returns the whole lines of a transcript after offset and
// the offset to continue from. A line still being written stays for the next
// read. An unread transcript starts far enough back to hold the entries
// written since since (transcriptWindowStart), and one that shrank starts
// again from its first byte.
func readTranscriptFrom(path string, offset int64, primed bool, since time.Time) ([]byte, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := fi.Size()
	switch {
	case !primed:
		offset = transcriptWindowStart(f, size, since)
	case offset > size:
		offset = 0
	}
	buf := make([]byte, size-offset)
	if _, err := f.ReadAt(buf, offset); err != nil && err != io.EOF {
		return nil, 0, err
	}
	cut := bytes.LastIndexByte(buf, '\n') + 1
	return buf[:cut], offset + int64(cut), nil
}

// transcriptProbe is how much is read at each step back to find a timestamp.
const transcriptProbe = 64 << 10

// transcriptWindowStart is where the first read of a transcript starts. It
// steps back queuedFirstRead at a time and reads one probe at each step, until
// an entry there is older than since. A busy session writes megabytes an hour,
// so a fixed tail can miss a prompt typed within the window.
func transcriptWindowStart(f *os.File, size int64, since time.Time) int64 {
	for off := size - queuedFirstRead; off > 0; off -= queuedFirstRead {
		if at, ok := transcriptTimeAt(f, off); ok && at.Before(since) {
			return off
		}
	}
	return 0
}

// transcriptTimeAt returns the timestamp of the first whole entry after off
// that has one.
func transcriptTimeAt(f *os.File, off int64) (time.Time, bool) {
	buf := make([]byte, transcriptProbe)
	n, err := f.ReadAt(buf, off)
	if err != nil && err != io.EOF {
		return time.Time{}, false
	}
	lines := bytes.Split(buf[:n], []byte{'\n'})
	// The first line may start mid entry, and the last may be cut off.
	for i := 1; i < len(lines)-1; i++ {
		var e struct {
			Timestamp string `json:"timestamp"`
		}
		if json.Unmarshal(lines[i], &e) != nil || e.Timestamp == "" {
			continue
		}
		if at, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
			return at, true
		}
	}
	return time.Time{}, false
}

// ownerTypedWhileBusy is the Stop hook's part: text the owner queued while the
// turn ran counts as an owner prompt for the gateway too, which closes the
// questions the session waited on. The first sweep of a transcript only keeps
// the words, as its text may be old. A command with no origin closes the wait
// as before but its text is not passed on as the owner's answer. policyCall
// reads a missing transcript entry again before this runs.
func (d *Daemon) ownerTypedWhileBusy(ctx context.Context, ls *localSession, sid string) {
	fresh, primed := d.sweepQueuedOwnerPrompts(sid)
	if !primed || len(fresh) == 0 {
		return
	}
	last := fresh[len(fresh)-1]
	text := ""
	if last.owner {
		text = last.text
	}
	d.ownerTyped(ctx, ls, sid, text)
}
