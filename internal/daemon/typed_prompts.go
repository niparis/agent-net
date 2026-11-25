package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
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

// Claude Code records a prompt given on its command line (`claude --bg -- <text>`)
// as typed by a human, so the transcript cannot tell a first message that
// agent-net, the CTO or a delivery restart wrote from the owner typing it. The
// daemon knows: it records each such message before the launch and binds it to
// the conversation once the launch names it. Owner effects skip a prompt that
// matches a mark.
//
// A mark is kept in daemon.db as well, so a hook that comes after a daemon
// restart still finds it. It holds a hash of the text, never the text.

// firstMessageKeep is how long a mark lasts. It covers the longest start and
// the owner prompt window, in which a prompt whose transcript entry came late
// is read again.
const firstMessageKeep = ownerPromptWindow + time.Hour

const firstMessagesSchema = `CREATE TABLE IF NOT EXISTS claude_first_messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT, text_hash TEXT NOT NULL, native TEXT NOT NULL DEFAULT '',
	job TEXT NOT NULL DEFAULT '', prompt_id TEXT NOT NULL DEFAULT '', ended INTEGER NOT NULL DEFAULT 0,
	version INTEGER NOT NULL DEFAULT 0, at INTEGER NOT NULL)`

// firstMark is one first message that the owner did not write.
type firstMark struct {
	id   int64 // the daemon.db row; 0 when it could not be written
	hash string
	// native is the conversation the message went to. It is empty while a
	// new conversation starts; a resume starts with the conversation it
	// continues, which Claude Code can replace with a new one.
	native string
	// job is the background job, known once `claude --bg` names it.
	job string
	// promptID is the prompt the hook saw for the message. A later prompt of
	// the same session with the same text is the owner's.
	promptID string
	// ended says native is final: the launch returned, or a prompt took the
	// mark. A mark an earlier daemon left unended belongs to a launch that
	// daemon never saw return.
	ended bool
	// version counts the changes, so a save that runs late never writes over
	// a newer one.
	version int64
	at      time.Time
	// launched is closed when this daemon's launch returns. It is nil for a
	// mark an earlier daemon wrote.
	launched chan struct{}
}

func (m *firstMark) launching() bool {
	if m.launched == nil {
		return false
	}
	select {
	case <-m.launched:
		return false
	default:
		return true
	}
}

// claim binds the mark to the first prompt that matches it.
func (m *firstMark) claim(promptID string) (match, changed bool) {
	switch {
	case m.promptID == "" && promptID != "":
		m.promptID = promptID
		return true, true
	case m.promptID == "":
		return true, false
	}
	return m.promptID == promptID, false
}

type nonOwnerFirst struct {
	mu    sync.Mutex
	marks []*firstMark
}

// prune drops the marks past firstMessageKeep. The caller holds mu.
func (f *nonOwnerFirst) prune(now time.Time) {
	kept := f.marks[:0]
	for _, m := range f.marks {
		if m.launching() || now.Sub(m.at) <= firstMessageKeep {
			kept = append(kept, m)
		}
	}
	clear(f.marks[len(kept):])
	f.marks = kept
}

// firstMessageHash is what a mark keeps of a text. Claude Code may add a
// system reminder to the prompt the hook sees, so reminders are left out.
func firstMessageHash(text string) string {
	text = strings.TrimSpace(protocol.WithoutSystemReminders(text))
	if text == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// firstMessageRow is the saved state of a mark, written after a change.
type firstMessageRow struct {
	id, version           int64
	native, job, promptID string
	ended                 bool
}

// changed counts a change and returns the state to save. The caller holds mu.
func (m *firstMark) changed() firstMessageRow {
	m.version++
	return firstMessageRow{id: m.id, version: m.version, native: m.native, job: m.job, promptID: m.promptID, ended: m.ended}
}

// loadFirstMessages reads the marks an earlier daemon left. New calls it.
func (d *Daemon) loadFirstMessages() error {
	if d.st == nil {
		return nil
	}
	marks, err := d.st.firstMessages(d.ownerPromptNow().Add(-firstMessageKeep))
	if err != nil {
		return err
	}
	d.nonOwnerFirst.mu.Lock()
	d.nonOwnerFirst.marks = append(marks, d.nonOwnerFirst.marks...)
	d.nonOwnerFirst.mu.Unlock()
	return nil
}

// beginFirstMessage records a first message before its launch runs, so a
// prompt hook that comes before the launch returns finds it. resume is the
// conversation the launch continues, when it is one.
func (d *Daemon) beginFirstMessage(text, resume string) *firstMark {
	now := d.ownerPromptNow()
	m := &firstMark{hash: firstMessageHash(text), native: resume, at: now, launched: make(chan struct{})}
	if d.st != nil {
		id, err := d.st.addFirstMessage(m.hash, resume, now, now.Add(-firstMessageKeep))
		if err != nil {
			d.Logf("claude start: the first message mark was not saved, a daemon restart loses it: %v", err)
		}
		m.id = id
	}
	f := &d.nonOwnerFirst
	f.mu.Lock()
	f.prune(now)
	f.marks = append(f.marks, m)
	f.mu.Unlock()
	return m
}

// firstMessageJob records the job of a running launch.
func (d *Daemon) firstMessageJob(m *firstMark, job string) {
	d.nonOwnerFirst.mu.Lock()
	m.job = job
	row := m.changed()
	d.nonOwnerFirst.mu.Unlock()
	d.saveFirstMessages([]firstMessageRow{row})
}

// endFirstMessage binds the mark to the conversation the launch started.
// native is empty when the launch failed: a failed launch that has a job keeps
// the mark, as the job can still run the prompt, and one without drops it.
func (d *Daemon) endFirstMessage(m *firstMark, native string) {
	f := &d.nonOwnerFirst
	f.mu.Lock()
	m.native, m.ended = native, true
	keep := native != "" || m.job != ""
	if !keep {
		for i, x := range f.marks {
			if x == m {
				f.marks = append(f.marks[:i], f.marks[i+1:]...)
				break
			}
		}
	}
	close(m.launched)
	row := m.changed()
	f.mu.Unlock()
	if !keep {
		if d.st != nil && m.id != 0 {
			if err := d.st.forgetFirstMessage(m.id); err != nil {
				d.Logf("claude start: could not drop a first message mark: %v", err)
			}
		}
		return
	}
	d.saveFirstMessages([]firstMessageRow{row})
}

func (d *Daemon) saveFirstMessages(rows []firstMessageRow) {
	if d.st == nil {
		return
	}
	for _, r := range rows {
		if r.id == 0 {
			continue
		}
		if err := d.st.setFirstMessage(r); err != nil {
			d.Logf("claude start: could not save a first message mark: %v", err)
		}
	}
}

// nonOwnerFirstPrompt says whether a Claude prompt is a first message that a
// launch passed and the owner did not write. wait lists the launches still
// running whose message has the same text: whether the prompt is theirs is
// known only when they return, so the caller waits and asks again.
func (d *Daemon) nonOwnerFirstPrompt(p protocol.PolicyRequest) (skip bool, wait []<-chan struct{}) {
	// Most prompts come when no mark is kept: they are not hashed.
	d.nonOwnerFirst.mu.Lock()
	none := len(d.nonOwnerFirst.marks) == 0
	d.nonOwnerFirst.mu.Unlock()
	if none {
		return false, nil
	}
	return d.firstMessageMatch(p.NativeID, p.PromptID, firstMessageHash(p.Prompt))
}

func (d *Daemon) firstMessageMatch(native, promptID, hash string) (skip bool, wait []<-chan struct{}) {
	if native == "" || hash == "" {
		return false, nil
	}
	f := &d.nonOwnerFirst
	var rows []firstMessageRow
	var byJob []*firstMark
	// take binds the mark to this prompt and its conversation. The caller
	// holds mu.
	take := func(m *firstMark) bool {
		ok, changed := m.claim(promptID)
		if ok && (m.native != native || !m.ended) {
			m.native, m.ended, changed = native, true, true
		}
		if changed {
			rows = append(rows, m.changed())
		}
		return ok
	}
	f.mu.Lock()
	f.prune(d.ownerPromptNow())
	for _, m := range f.marks {
		if m.hash != hash {
			continue
		}
		switch {
		case m.launching() && m.native == native:
			// A resume of this conversation that has not returned. The
			// launch sets the conversation it ends with.
			ok, changed := m.claim(promptID)
			if changed {
				rows = append(rows, m.changed())
			}
			skip = ok
		case m.launching():
			// A new conversation, or a resume that Claude Code can give a
			// new ID: wait for the launch to name it.
			wait = append(wait, m.launched)
		case m.native == native:
			skip = take(m)
		case m.job != "" && (m.native == "" || !m.ended):
			// A failed launch whose job is up, or a launch an earlier daemon
			// never saw return: the job's session file names the
			// conversation.
			byJob = append(byJob, m)
		case !m.ended:
			// An earlier daemon stopped before `claude --bg` named the job,
			// so nothing names the conversation. The first prompt with the
			// text takes the mark: losing the owner's words is safe, taking
			// an agent's text for the owner's is not.
			skip = take(m)
		}
		if skip {
			break
		}
	}
	f.mu.Unlock()
	// The session files are read outside the lock.
	for _, m := range byJob {
		if skip {
			break
		}
		if sf, ok := findClaudeBg(d.cfg.ClaudeSessionsDir, m.job); ok && sf.SessionID == native {
			f.mu.Lock()
			skip = take(m)
			f.mu.Unlock()
		}
	}
	d.saveFirstMessages(rows)
	if skip {
		return true, nil
	}
	return false, wait
}

func (s *state) addFirstMessage(hash, native string, at, expired time.Time) (int64, error) {
	if _, err := s.db.Exec(`DELETE FROM claude_first_messages WHERE at < ?`, expired.UnixNano()); err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO claude_first_messages(text_hash,native,at) VALUES(?,?,?)`, hash, native, at.UnixNano())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *state) setFirstMessage(r firstMessageRow) error {
	_, err := s.db.Exec(`UPDATE claude_first_messages SET native=?, job=?, prompt_id=?, ended=?, version=? WHERE id=? AND version<?`,
		r.native, r.job, r.promptID, r.ended, r.version, r.id, r.version)
	return err
}

func (s *state) forgetFirstMessage(id int64) error {
	_, err := s.db.Exec(`DELETE FROM claude_first_messages WHERE id=?`, id)
	return err
}

// firstMessages drops the marks older than since and returns the rest.
func (s *state) firstMessages(since time.Time) ([]*firstMark, error) {
	if _, err := s.db.Exec(`DELETE FROM claude_first_messages WHERE at < ?`, since.UnixNano()); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, text_hash, native, job, prompt_id, ended, version, at FROM claude_first_messages ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*firstMark
	for rows.Next() {
		m := &firstMark{}
		var at int64
		if err := rows.Scan(&m.id, &m.hash, &m.native, &m.job, &m.promptID, &m.ended, &m.version, &at); err != nil {
			return nil, err
		}
		m.at = time.Unix(0, at)
		out = append(out, m)
	}
	return out, rows.Err()
}
