package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/niparis/agent-net/internal/daemon"
)

// Claude Code writes a system entry when it keeps a peer message for review
// instead of showing it to the session. The entry names the reply socket of
// the sender, so the adapter can match it to the delivery record and report
// the message as held. When the owner approves it, Claude Code writes the
// message as a user entry, which is the release.
const (
	// heldLookback is how much of an existing transcript the first read looks
	// at. A hold is recent; older text is not news.
	heldLookback = 1 << 20
)

var (
	// A reply socket is .../r/<sender>.<conversation>.sock, or .../r/<sender>.sock
	// for a message without a conversation.
	heldReplySock = regexp.MustCompile(`/r/([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+)\.sock`)
	heldSoloSock  = regexp.MustCompile(`/r/([A-Za-z0-9_-]+)\.sock`)
	// A peer frame names its message ID in the agent-net note.
	heldMessageID = regexp.MustCompile(`\bm_[0-9a-f]{8,}\b`)
)

// heldWatch is how far one transcript was read.
type heldWatch struct {
	path        string
	offset      int64
	partial     []byte
	initialized bool
}

// transcriptEntry is the part of a Claude transcript line the hold check
// reads. A system entry carries its text in content; a user entry carries a
// message with string or block content.
type transcriptEntry struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// startHeldScan runs one transcript scan in the background. The poll loop
// must not wait on the gateway, and one scan at a time keeps a hold and its
// release in order.
func (a *Adapter) startHeldScan(byNative map[string]found) {
	a.mu.Lock()
	if a.heldScanning {
		a.mu.Unlock()
		return
	}
	a.heldScanning = true
	work := make(map[string]found, len(byNative))
	for k, v := range byNative {
		work[k] = v
	}
	a.mu.Unlock()
	go func() {
		defer func() {
			a.mu.Lock()
			a.heldScanning = false
			a.mu.Unlock()
		}()
		a.scanHeld(work)
	}()
}

// scanHeld reads what Claude Code appended to each session transcript since
// the last poll. It reports a new hold and a release to the daemon. It never
// reads the whole transcript again, and never holds the adapter lock across a
// read.
func (a *Adapter) scanHeld(byNative map[string]found) {
	keep := map[string]bool{}
	for native, f := range byNative {
		path := daemon.ClaudeTranscriptPath(a.cfg.ProjectsDir, native, f.info.Cwd)
		if path == "" {
			continue
		}
		keep[path] = true
		a.scanHeldTranscript(native, path)
	}
	a.mu.Lock()
	for path := range a.heldWatches {
		if !keep[path] {
			delete(a.heldWatches, path)
		}
	}
	a.mu.Unlock()
}

func (a *Adapter) scanHeldTranscript(native, path string) {
	// Without the gateway ID the daemon cannot report a hold. Do not advance
	// the read: the next poll after the session registers sees the same text.
	recipient := a.host.SessionID("claude", native)
	if recipient == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	a.mu.Lock()
	if a.heldWatches == nil {
		a.heldWatches = map[string]*heldWatch{}
	}
	w := a.heldWatches[path]
	if w == nil {
		w = &heldWatch{path: path, offset: max(info.Size()-heldLookback, 0)}
		a.heldWatches[path] = w
	}
	if !w.initialized {
		w.initialized = true
	}
	if info.Size() < w.offset {
		// The transcript was rewritten. Read it from its start.
		w.offset, w.partial = 0, nil
	}
	if info.Size() == w.offset {
		a.mu.Unlock()
		return
	}
	offset, size := w.offset, info.Size()
	a.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return
	}
	buf := make([]byte, size-offset)
	n, _ := f.ReadAt(buf, offset)
	f.Close()
	buf = buf[:n]

	a.mu.Lock()
	lines := append(w.partial, buf...)
	last := bytes.LastIndexByte(lines, '\n')
	if last < 0 {
		w.partial = append([]byte(nil), lines...)
		w.offset = size
		a.mu.Unlock()
		return
	}
	w.partial = append([]byte(nil), lines[last+1:]...)
	w.offset = size - int64(len(w.partial))
	done := lines[:last]
	ctx := a.ctx
	a.mu.Unlock()

	for _, line := range bytes.Split(done, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var e transcriptEntry
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		switch e.Type {
		case "system":
			if !heldEntry(e.Content) {
				continue
			}
			sender, conv, ok := heldAddress(e.Content)
			if !ok {
				a.host.Logf("claude: a held message in %s names no reply socket: %q", native, firstLine(e.Content))
				continue
			}
			a.host.ClaudeHeld(ctx, recipient, sender, conv, parseTranscriptTime(e.Timestamp))
		case "user":
			text := contentText(e.Message.Content)
			if !strings.Contains(text, "<cross-session-message") {
				continue
			}
			sender, conv, _ := heldAddress(text)
			ids := heldMessageIDs(text)
			if len(ids) == 0 {
				// A batch carrier names no original ID. A release of it
				// releases every held message from that sender.
				if sender != "" {
					a.host.ClaudeHeldReleased(ctx, recipient, "", sender, conv)
				}
				continue
			}
			for _, id := range ids {
				a.host.ClaudeHeldReleased(ctx, recipient, id, sender, conv)
			}
		}
	}
}

// heldEntry says whether a system entry is Claude Code's hold notice. Both
// wordings seen in transcripts are accepted.
func heldEntry(s string) bool {
	if !strings.Contains(s, "not delivered to Claude") {
		return false
	}
	return strings.Contains(s, "Held peer message") || strings.Contains(s, "Held message from another session")
}

// heldAddress reads the sender and conversation from the reply socket the
// hold notice names.
func heldAddress(s string) (sender, conversation string, ok bool) {
	if m := heldReplySock.FindStringSubmatch(s); m != nil {
		return m[1], m[2], true
	}
	if m := heldSoloSock.FindStringSubmatch(s); m != nil {
		return m[1], "", true
	}
	return "", "", false
}

// heldMessageIDs returns the unique message IDs a released user entry names.
func heldMessageIDs(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range heldMessageID.FindAllString(s, -1) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func parseTranscriptTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
