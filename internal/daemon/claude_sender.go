package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// claudeSender says who sent a Claude Code prompt, as Claude Code records it
// in the transcript entry of that prompt.
type claudeSender int

const (
	// senderUnknown: the entry names no origin. The owner's effects apply, as
	// they did before this check.
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

// whenClaudeOwnerPrompt runs apply unless the prompt was sent by a schedule, a
// peer, Claude Code itself or the daemon. The prompt hook runs before Claude Code writes
// the prompt to the transcript, and the docs say the file may lag, so the
// entry is usually missing when the hook runs. The check then waits in the
// background. A prompt without a usable transcript gets the old answer at once.
func (d *Daemon) whenClaudeOwnerPrompt(ctx context.Context, p protocol.PolicyRequest, apply func(context.Context)) {
	// Claude Code records a line the daemon typed itself as a human prompt, so
	// the transcript cannot tell it from the owner. The daemon knows it.
	if d.daemonTyped.typed(p.NativeID, p.Prompt) {
		return
	}
	if !claudeTranscriptFor(p.TranscriptPath, p.NativeID, p.PromptID) {
		apply(ctx)
		return
	}
	if s, found := claudeSenderOf(p.TranscriptPath, p.NativeID, p.PromptID); found {
		if s != senderOther {
			apply(ctx)
		}
		return
	}
	d.claudeChecks.Add(1)
	go func() {
		defer d.claudeChecks.Done()
		if claudeSenderWithin(p.TranscriptPath, p.NativeID, p.PromptID, claudeSenderWait) != senderOther {
			apply(context.Background())
		}
	}()
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
