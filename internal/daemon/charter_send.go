package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
