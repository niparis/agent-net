package daemon

import (
	"strings"
	"sync"
	"time"
)

// daemonTypedTTL is how long a prompt the daemon typed stays known. Claude Code
// runs the prompt hook within moments of the typing; a minute is far longer
// than that and far shorter than the owner could type the same text himself.
const daemonTypedTTL = time.Minute

// daemonTyped remembers the text the daemon typed into a session's terminal
// (the idle compaction command). Claude Code records such a line as a human
// prompt, so the owner effects must skip it. The entry is not consumed: the
// prompt hook for one prompt can run more than one owner check.
type daemonTyped struct {
	mu    sync.Mutex
	until map[string]time.Time // session native ID + NUL + text
	now   func() time.Time     // test hook
}

func typedKey(nativeID, text string) string { return nativeID + "\x00" + strings.TrimSpace(text) }

func (t *daemonTyped) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *daemonTyped) note(nativeID, text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock()
	for k, u := range t.until {
		if !u.After(now) {
			delete(t.until, k)
		}
	}
	if t.until == nil {
		t.until = map[string]time.Time{}
	}
	t.until[typedKey(nativeID, text)] = now.Add(daemonTypedTTL)
}

func (t *daemonTyped) typed(nativeID, text string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	u, ok := t.until[typedKey(nativeID, text)]
	return ok && u.After(t.clock())
}

// NoteTypedPrompt records text an adapter is about to type into a session's
// terminal, so the policy check does not take it for the owner typing.
func (d *Daemon) NoteTypedPrompt(harness, nativeID, text string) {
	if harness == "claude" {
		d.daemonTyped.note(nativeID, text)
	}
}
