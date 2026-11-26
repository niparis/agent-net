package gateway

import (
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// maxOwnerWordsPerSession and maxOwnerWordsBytes bound the store, the same
// bounds a machine keeps in memory.
const (
	maxOwnerWordsPerSession = 20
	maxOwnerWordsBytes      = 16 * 1024
)

// ownerWords keeps what the owner typed in each session for
// protocol.OwnerWordsWindow, so a worker start's owner_words quote counts
// whichever session or machine he typed it in. It lives in memory only: his
// words are never written to gateway.db, so no backup copies them. A restart
// forgets them, and each machine sends its own again when it reconnects. mu
// is a leaf lock, never held across a call.
type ownerWords struct {
	mu sync.Mutex
	by map[string][]ownerWordsEntry
}

type ownerWordsEntry struct {
	at   int64
	text string
}

// dropExpired removes the entries older than since. Callers hold mu.
func (o *ownerWords) dropExpired(since int64) {
	for session, list := range o.by {
		kept := list[:0]
		for _, e := range list {
			if e.at >= since {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(o.by, session)
		} else {
			o.by[session] = kept
		}
	}
}

// KeepOwnerWords keeps a prompt the owner typed in a session. The machine has
// already removed pasted blocks and the turns a harness injects; an injected
// turn that still reaches here is dropped. Words older than the window are
// not kept, and keeping new ones drops the expired entries.
func (s *Store) KeepOwnerWords(session, text string, at time.Time) error {
	text = strings.TrimSpace(text)
	if session == "" || text == "" || protocol.InjectedClaudePrompt(text) {
		return nil
	}
	if len(text) > maxOwnerWordsBytes {
		text = strings.ToValidUTF8(text[:maxOwnerWordsBytes], "")
	}
	now := s.now()
	if at.IsZero() || at.After(now) {
		at = now
	}
	since := now.Add(-protocol.OwnerWordsWindow).UnixNano()
	if at.UnixNano() < since {
		return nil
	}
	o := &s.ownerWords
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dropExpired(since)
	if o.by == nil {
		o.by = map[string][]ownerWordsEntry{}
	}
	list := o.by[session]
	// A machine sends its words again when it reconnects: a prompt is kept
	// once, at its latest time.
	for i, e := range list {
		if e.text == text {
			list[i].at = max(e.at, at.UnixNano())
			return nil
		}
	}
	list = append(list, ownerWordsEntry{at: at.UnixNano(), text: text})
	if len(list) > maxOwnerWordsPerSession {
		list = append([]ownerWordsEntry(nil), list[len(list)-maxOwnerWordsPerSession:]...)
	}
	o.by[session] = list
	return nil
}

// FindOwnerWords finds the quote in what the owner typed in any session in
// the last window, ignoring case and white space. It names the latest prompt
// that holds it, and never returns the text.
func (s *Store) FindOwnerWords(quote string) (protocol.OwnerWordsFound, error) {
	since := s.now().Add(-protocol.OwnerWordsWindow).UnixNano()
	o := &s.ownerWords
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dropExpired(since)
	var best protocol.OwnerWordsFound
	for session, list := range o.by {
		for _, e := range list {
			if e.at > best.At && protocol.OwnerWordsMatch(quote, []string{e.text}) {
				best = protocol.OwnerWordsFound{Found: true, Session: session, At: e.at}
			}
		}
	}
	return best, nil
}
