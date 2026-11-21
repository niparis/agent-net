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

// queuedFirstRead caps the first read of a transcript. Older text is outside
// the ownerPromptWindow in practice.
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

// queuedOwnerText returns what the owner typed when the line is a queued
// command he sent. A command Claude Code or agent-net sent (a task notice, a
// peer message, a scheduled turn, an injected reminder) has an origin that is
// not the human's, or a mode that is not a plain prompt, and gives nothing.
func queuedOwnerText(line []byte) (text string, at time.Time, ok bool) {
	if !bytes.Contains(line, queuedMark) {
		return "", time.Time{}, false
	}
	var e queuedAttachment
	if json.Unmarshal(line, &e) != nil || e.Type != "attachment" || e.Attachment.Type != "queued_command" {
		return "", time.Time{}, false
	}
	if o := e.Attachment.Origin; o != nil && o.Kind != "" && o.Kind != "human" {
		return "", time.Time{}, false
	}
	if m := e.Attachment.CommandMode; m != "" && m != "prompt" {
		return "", time.Time{}, false
	}
	text = strings.TrimSpace(queuedPromptText(e.Attachment.Prompt))
	if text == "" || !isHumanClaudePrompt(text) {
		return "", time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return "", time.Time{}, false
	}
	return text, at, true
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
		d.cto.mu.Lock()
		defer d.cto.mu.Unlock()
		return d.claudeCTO.session
	}
	if ls := d.sessionByID(session); ls != nil {
		if info := d.infoOf(ls); info.Harness == "claude" {
			return info.NativeID
		}
	}
	return ""
}

// sweepQueuedOwnerPrompts keeps the owner's queued prompts of a session that
// were written to its transcript since the last sweep. It returns the new
// ones, oldest first, and whether an earlier sweep had already read this
// transcript (the first read finds old text, which is not news).
func (d *Daemon) sweepQueuedOwnerPrompts(session string) (fresh []string, primed bool) {
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
	d.ownerPrompts.sweepMu.Lock()
	defer d.ownerPrompts.sweepMu.Unlock()
	off, primed := d.ownerPrompts.scanned[path]
	buf, end, err := readTranscriptFrom(path, off, primed)
	if err != nil {
		return nil, primed
	}
	if d.ownerPrompts.scanned == nil {
		d.ownerPrompts.scanned = map[string]int64{}
	}
	d.ownerPrompts.scanned[path] = end
	now := d.ownerPromptNow()
	for _, line := range bytes.Split(buf, []byte{'\n'}) {
		text, at, ok := queuedOwnerText(line)
		if !ok || now.Sub(at) > ownerPromptWindow {
			continue
		}
		d.rememberOwnerPromptAt(session, text, at)
		fresh = append(fresh, text)
	}
	return fresh, primed
}

// readTranscriptFrom returns the whole lines of a transcript after offset and
// the offset to continue from. A line still being written stays for the next
// read. An unread transcript starts at its end minus queuedFirstRead, and one
// that shrank starts again from its first byte.
func readTranscriptFrom(path string, offset int64, primed bool) ([]byte, int64, error) {
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
		offset = max(size-queuedFirstRead, 0)
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

// ownerTypedWhileBusy is the Stop hook's part: text the owner queued while the
// turn ran counts as an owner prompt for the gateway too, which closes the
// questions the session waited on. The first sweep of a transcript only keeps
// the words, as its text may be old.
func (d *Daemon) ownerTypedWhileBusy(ctx context.Context, ls *localSession, sid string) {
	fresh, primed := d.sweepQueuedOwnerPrompts(sid)
	if primed && len(fresh) > 0 {
		d.ownerTyped(ctx, ls, sid, fresh[len(fresh)-1])
	}
}
