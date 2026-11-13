package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/niparis/agent-net/internal/protocol"
)

// The charter block of a Claude Code session. Every hook answer stays in the
// transcript, so the full block is sent once and only again when its text
// changes or a compaction may have dropped it. The state is per session and
// lives here, in the daemon, not in the session: the hook process is short.
type charterSendState struct {
	mu sync.Mutex
	// sent is the hash of the last block sent and the compaction generation
	// it was sent in.
	sent map[string]charterSent
	// generation counts the compactions of a session. A change sends the
	// block again on the next prompt.
	generation map[string]uint64
	// notes is, per session, the title lines of the notes index it was last
	// sent, and the compaction generation of that send.
	notes map[string]notesSent
}

type notesSent struct {
	titles map[string]string // key (heading and name) to the title line
	gen    uint64
}

type charterSent struct {
	hash string
	gen  uint64
}

// charterHash is the hash of the charter text without the change line.
func charterHash(charter string) string {
	sum := sha256.Sum256([]byte(charter))
	return hex.EncodeToString(sum[:])
}

// take says whether this turn gets the block. changed is true when a block
// was sent before and its text is now different; the caller then puts the one
// change line before the new text. The first send is not a change.
func (c *charterSendState) take(sid, charter string) (send, changed bool) {
	hash := charterHash(charter)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sent == nil {
		c.sent = map[string]charterSent{}
	}
	gen := c.generation[sid]
	st, have := c.sent[sid]
	if !have || st.hash != hash {
		c.sent[sid] = charterSent{hash: hash, gen: gen}
		return true, have
	}
	if st.gen != gen {
		st.gen = gen
		c.sent[sid] = st
		return true, false
	}
	return false, false
}

// compacted starts a new generation for a session, so its next prompt gets
// the full block again.
func (c *charterSendState) compacted(sid string) {
	c.mu.Lock()
	if c.generation == nil {
		c.generation = map[string]uint64{}
	}
	c.generation[sid]++
	c.mu.Unlock()
}

// compactionCall serves MethodCompaction. Only the Claude Code hook (the
// command line) may say that a session compacted. A session this daemon does
// not know is ignored: the hook never fails a session.
func (lc *localConn) compactionCall(client string, raw json.RawMessage) (any, error) {
	if client != "cli" {
		return nil, errors.New("compaction is for the Claude Code hook")
	}
	var p protocol.CompactionRequest
	if json.Unmarshal(raw, &p) != nil || p.NativeID == "" || (p.Harness != "" && p.Harness != "claude") {
		return nil, errors.New("bad compaction params")
	}
	d := lc.d
	d.mu.Lock()
	ls := d.sessions[key("claude", p.NativeID)]
	d.mu.Unlock()
	if ls == nil {
		return struct{}{}, nil
	}
	if sid := d.currentSessionID(ls); sid != "" {
		d.charterSends.compacted(sid)
	}
	return struct{}{}, nil
}

// charterBlockForTurn returns the charter text the Claude Code hook prints
// this turn, or "" when the block is already in the transcript. An empty
// charter changes no state.
func (d *Daemon) charterBlockForTurn(sid, charter string) string {
	if charter == "" {
		return ""
	}
	send, changed := d.charterSends.take(sid, charter)
	if !send {
		return ""
	}
	if changed {
		return protocol.CharterChangedNotice + "\n" + charter
	}
	return charter
}

// notesTitles reads the title lines of a notes index. The key of a line is the
// heading it is under and its name, so the same name in two scopes is two keys.
func notesTitles(index string) map[string]string {
	out := map[string]string{}
	heading := ""
	for _, line := range strings.Split(index, "\n") {
		switch {
		case strings.HasPrefix(line, "- "):
			name, _, _ := strings.Cut(strings.TrimPrefix(line, "- "), " (")
			out[heading+"|"+name] = line
		case strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "["):
			heading = line
		}
	}
	return out
}

// notesNoticeMaxLines keeps a notes notice short: more changed titles than
// this are counted, and the session reads the rest with agent_notes_find.
const notesNoticeMaxLines = 8

// notesBlockForTurn is the notes text the Claude Code hook prints this turn.
// The whole index goes in on the first prompt and after a compaction. Later,
// only a change to a title (a new note, a new description, a note that came in
// or left the list) sends anything, and then only the changed titles. A new
// body with the same title sends nothing: the session was not told the body.
func (d *Daemon) notesBlockForTurn(sid, index string) string {
	c := &d.charterSends
	titles := notesTitles(index)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.notes == nil {
		c.notes = map[string]notesSent{}
	}
	gen := c.generation[sid]
	prev, have := c.notes[sid]
	c.notes[sid] = notesSent{titles: titles, gen: gen}
	if !have || prev.gen != gen {
		return index
	}
	var lines []string
	for key, line := range titles {
		if prev.titles[key] != line {
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	var gone []string
	for key := range prev.titles {
		if _, ok := titles[key]; !ok {
			_, name, _ := strings.Cut(key, "|")
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	for _, name := range gone {
		lines = append(lines, "- no longer listed: "+name)
	}
	if len(lines) == 0 {
		return ""
	}
	more := 0
	if len(lines) > notesNoticeMaxLines {
		more = len(lines) - notesNoticeMaxLines
		lines = lines[:notesNoticeMaxLines]
	}
	text := "[agent-net] Your notes changed. Changed titles:\n" + strings.Join(lines, "\n")
	if more > 0 {
		text += fmt.Sprintf("\n%d more changed: list them with agent_notes_find.", more)
	}
	return text
}
