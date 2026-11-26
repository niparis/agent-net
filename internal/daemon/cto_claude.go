package daemon

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/niparis/agent-net/internal/claudetext"
	"github.com/niparis/agent-net/internal/protocol"
	"github.com/niparis/agent-net/internal/redact"
)

// claudeCTOState runtime fields are protected by cto.mu. The process root and
// starting flag are atomic. capMu guards the capability cache and the probes in
// flight, which a start from the owner and one from a proposal note both use.
// No probe runs while capMu is held.
type claudeCTOState struct {
	root         atomic.Pointer[claudeCTORoot]
	starting     atomic.Bool
	capMu        sync.Mutex
	gen          uint64
	token        string
	ref, model   string
	session      atomic.Pointer[string]
	capabilities map[string]claudeCTOCapability
	// capProbing has one entry per binary whose probe is running. The callers
	// that arrive meanwhile wait for it, so a binary is probed once at a time.
	capProbing map[string]chan struct{}
	restarted  bool
	// stopping holds the jobs whose stop runs outside cto.mu. Their process
	// and session file live until `claude stop` returns, and an adoption must
	// not bind them again in that time.
	stopping map[string]bool
}

func (c *claudeCTOState) setSession(id string) { c.session.Store(&id) }

func (c *claudeCTOState) sessionID() string {
	if id := c.session.Load(); id != nil {
		return *id
	}
	return ""
}

type claudeCTOCapability struct {
	mtime time.Time
	err   error
}

type claudeCTORoot struct {
	pid   int
	token string
}

type claudeCTOHarness struct{}

func (claudeCTOHarness) name() string { return "claude" }

// IsClaudeCTOJob says whether job is the Claude background job bound as the
// CTO's conversation. The Claude adapter compacts that job at the cto agent
// level, and the owner's open attach client does not stop it.
func (d *Daemon) IsClaudeCTOJob(job string) bool {
	if job == "" {
		return false
	}
	d.cto.mu.Lock()
	defer d.cto.mu.Unlock()
	return job == d.claudeCTO.ref
}

// releaseClaudeCTOLocked forgets the CTO's Claude job without ending it.
func (d *Daemon) releaseClaudeCTOLocked() {
	c := &d.claudeCTO
	c.root.Store(nil)
	c.ref, c.model, c.token = "", "", ""
	c.setSession("")
}

// detachClaudeCTOLocked forgets the Claude run under cto.mu and returns the
// rest of its stop: `claude stop` and the kill, which call other processes and
// run after cto.mu is released (B12). The generation changes at once, so a
// start or a write back that began before the stop drops its result.
func (d *Daemon) detachClaudeCTOLocked(reason string) ctoStop {
	c := &d.claudeCTO
	c.gen++
	root := c.root.Load()
	ref := c.ref
	c.root.Store(nil)
	c.ref, c.model, c.token = "", "", ""
	c.setSession("")
	if ref == "" && root == nil {
		return nil
	}
	if ref != "" {
		if c.stopping == nil {
			c.stopping = map[string]bool{}
		}
		c.stopping[ref] = true
	}
	return ctoStop{func() {
		if ref != "" && reason == "crash" {
			state, detail := claudeCTOJobState(d.cfg.ClaudeSessionsDir, ref)
			d.Logf("cto.crash harness=claude reason=job_ended state=%q detail=%q", claudeCTOErrorOutput([]byte(state)), detail)
		}
		(claudeLauncher{}).stopID(d, ref)
		if root != nil {
			token, alive := procToken(root.pid)
			if alive && token == root.token {
				pgid, err := syscall.Getpgid(root.pid)
				if err == nil && pgid == root.pid {
					_ = syscall.Kill(-root.pid, syscall.SIGKILL)
				} else {
					_ = syscall.Kill(root.pid, syscall.SIGKILL)
				}
			}
		}
		if ref == "" {
			return
		}
		d.cto.mu.Lock()
		delete(c.stopping, ref)
		d.cto.mu.Unlock()
		d.Logf("cto.stop harness=claude reason=%s", reason)
		d.scheduleCTOPresence()
	}}
}

// stopClaudeCTO stops the Claude CTO. The caller does not hold cto.mu.
func (d *Daemon) stopClaudeCTO(reason string) {
	d.cto.mu.Lock()
	stop := d.detachClaudeCTOLocked(reason)
	d.cto.mu.Unlock()
	stop.run()
}

func sameCTODir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if real, err := filepath.EvalSymlinks(a); err == nil {
		a = real
	}
	if real, err := filepath.EvalSymlinks(b); err == nil {
		b = real
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// findClaudeCTOBg binds only the background job launched in the private CTO
// working directory. The PID file name must match the session's PID. An empty
// ref allows adoption of any matching live job after a daemon restart.
func findClaudeCTOBg(sessions, dir, ref string) (claudeSessionFile, bool) {
	entries, err := os.ReadDir(sessions)
	if err != nil {
		return claudeSessionFile{}, false
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(sessions, entry.Name()))
		if err != nil {
			continue
		}
		var sf struct {
			claudeSessionFile
			Cwd  string `json:"cwd"`
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &sf) != nil || sf.PID <= 1 || entry.Name() != strconv.Itoa(sf.PID)+".json" || sf.SessionID == "" || sf.Kind != "bg" || (sf.JobID == "" || (ref != "" && sf.JobID != ref)) || sf.Name != "agent-net-cto" || !sameCTODir(sf.Cwd, dir) {
			continue
		}
		if _, alive := procToken(sf.PID); alive {
			return sf.claudeSessionFile, true
		}
	}
	return claudeSessionFile{}, false
}

const claudeCTORestartNotice = "The CTO's Claude conversation was closed by Claude Code; started a new one."

// setClaudeCTORestarted takes cto.mu, which guards restarted: cto_enter reads
// it while a start may still be running.
func (d *Daemon) setClaudeCTORestarted(v bool) {
	d.cto.mu.Lock()
	d.claudeCTO.restarted = v
	d.cto.mu.Unlock()
}

// withoutEnvKey returns env without any entry for key.
func withoutEnvKey(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k != key {
			out = append(out, kv)
		}
	}
	return out
}

// Terminal failure states are authoritative: a session file can appear before a resume fails.
// Claude Code writes {"state": ..., "detail": ...}; older builds used status
// and error, so both are read.
func claudeCTOJobState(sessions, ref string) (string, string) {
	if filepath.Base(ref) != ref {
		return "", ""
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(sessions), "jobs", ref, "state.json"))
	if err != nil {
		return "", ""
	}
	var state struct {
		State  string          `json:"state"`
		Detail json.RawMessage `json:"detail"`
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return "", ""
	}
	status := state.State
	if status == "" {
		status = state.Status
	}
	detail := strings.Trim(string(state.Detail), `"`)
	if detail == "" || detail == "null" {
		detail = strings.Trim(string(state.Error), `"`)
	}
	if detail == "" || detail == "null" {
		detail = status
	}
	return strings.ToLower(status), claudeCTOErrorOutput([]byte(detail))
}

// claudeJobEnded reports a job state that means the background job is gone.
// A live job can be done between turns. For that and unknown states, the
// process check decides whether it is still alive.
func claudeJobEnded(state string) bool {
	switch state {
	case "failed", "crashed", "killed", "stopped":
		return true
	}
	return false
}

var claudeVersionPattern = regexp.MustCompile(`\b[0-9]+\.[0-9]+\.[0-9]+\b`)

// checkClaudeCTOCapabilities returns an error when the Claude binary lacks a
// flag the CTO needs. The result is cached per binary and modification time.
// One probe runs per binary at a time: a caller that arrives meanwhile waits for
// it. The lock is not held while a probe runs (B12).
func (d *Daemon) checkClaudeCTOCapabilities(ctx context.Context, bin string) error {
	c := &d.claudeCTO
	info, err := os.Stat(bin)
	if err != nil {
		return fmt.Errorf("check Claude Code: %w", err)
	}
	for {
		c.capMu.Lock()
		if cached, ok := c.capabilities[bin]; ok && cached.mtime.Equal(info.ModTime()) {
			c.capMu.Unlock()
			return cached.err
		}
		if probing, ok := c.capProbing[bin]; ok {
			c.capMu.Unlock()
			select {
			case <-probing:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if c.capProbing == nil {
			c.capProbing = make(map[string]chan struct{})
		}
		done := make(chan struct{})
		c.capProbing[bin] = done
		c.capMu.Unlock()

		cacheable, result := d.probeClaudeCTO(ctx, bin)

		c.capMu.Lock()
		delete(c.capProbing, bin)
		if cacheable {
			if c.capabilities == nil {
				c.capabilities = make(map[string]claudeCTOCapability)
			}
			c.capabilities[bin] = claudeCTOCapability{mtime: info.ModTime(), err: result}
		}
		c.capMu.Unlock()
		close(done)
		return result
	}
}

// probeClaudeCTO runs the help and version commands of bin and checks its help
// for the flags the CTO needs. It reports cacheable false when a command could
// not run, so the next caller probes again.
func (d *Daemon) probeClaudeCTO(ctx context.Context, bin string) (cacheable bool, result error) {
	help, err := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("check Claude Code help: %w", err)
	}
	version, versionErr := exec.CommandContext(ctx, bin, "--version").CombinedOutput()
	v := claudeVersionPattern.FindString(string(version))
	if versionErr != nil || v == "" {
		v = "(unknown)"
	}
	if d.log != nil {
		d.log.Printf("CTO Claude Code version: %s", v)
	}
	for _, flag := range []string{"--strict-mcp-config"} {
		present := false
		for _, word := range strings.Fields(string(help)) {
			if strings.TrimSuffix(word, ",") == flag {
				present = true
				break
			}
		}
		if !present {
			return true, fmt.Errorf("CTO Claude: required capability %s is missing from --help; run `claude update`", flag)
		}
	}
	return true, nil
}

func claudeTrustPrompt(output []byte) bool {
	text := strings.ToLower(string(output))
	return strings.Contains(text, "trust") && (strings.Contains(text, "workspace") || strings.Contains(text, "project") || strings.Contains(text, "folder"))
}

func claudeCTOErrorOutput(output []byte) string {
	lines := strings.Split(string(output), "\n")
	if len(lines) > 2 {
		lines = lines[:2]
	}
	for i, line := range lines {
		lines[i] = redact.Args(strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, line)))
	}
	return redact.Cut(redact.Secrets(strings.TrimSpace(strings.Join(lines, " · "))), 500)
}

// ctoFrameNamePattern keeps a frame attribute to letters, digits, "_", "." and
// "-", so a value cannot close the frame it is written into.
var ctoFrameNamePattern = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// CTOFirstPrompt is the text of one message to the Claude CTO. The owner's
// frame is used only when label is empty; every other sender gets agent-net's
// labelled frame. The inbox delivery writes it as a frame, and a restart for
// delivery gives it as the first prompt, so every copy of a message names the
// same sender and carries msgID.
func CTOFirstPrompt(msgID, label, text string) string {
	id := ctoFrameNamePattern.ReplaceAllString(msgID, "-")
	if label == "" {
		return fmt.Sprintf("<cross-session-message from-name=\"owner\" from-mode=\"prompting\" msg-id=\"%s\">\n%s\n\n[agent-net] This is the owner speaking. Please answer in this conversation.\n</cross-session-message>", id, CTOFrameText(text))
	}
	return fmt.Sprintf("<cross-session-message from-name=\"agent-net\" from-mode=\"prompting\" msg-id=\"%s\">\n[agent-net: %s]\n\n%s\n</cross-session-message>", id, CTOFrameText(label), CTOFrameText(text))
}

// CTOFrameText stops text from closing or opening a cross-session frame. The
// Claude adapter uses it for every frame, so a body cannot fake the wrapper.
func CTOFrameText(text string) string {
	text = protocol.NeutralizeMarkers(text)
	text = strings.ReplaceAll(text, "</cross-session-message", "</​cross-session-message")
	return strings.ReplaceAll(text, "<cross-session-message", "<​cross-session-message")
}

func (claudeCTOHarness) send(ctx context.Context, d *Daemon, text string) (any, error) {
	return d.claudeCTOSend(ctx, text, "")
}

func (claudeCTOHarness) sendSystem(ctx context.Context, d *Daemon, label, text string) (any, error) {
	return d.claudeCTOSend(ctx, text, label)
}

// claudeCTOSend delivers text to the CTO's Claude conversation. An empty label
// is the owner's words; a label marks agent-net's own prompt.
func (d *Daemon) claudeCTOSend(ctx context.Context, text, label string) (any, error) {
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return nil, err
	}
	model := settings.ClaudeModel
	id, err := d.claudeCTOSession()
	if err != nil {
		return nil, err
	}
	if err = d.startClaudeRole(ctx, roleCTO, id, model); err != nil {
		return nil, err
	}
	d.cto.mu.Lock()
	oldID := id
	id = d.claudeCTO.sessionID()
	d.cto.mu.Unlock()
	if err = d.saveClaudeCTOSession(oldID, id); err != nil {
		return nil, err
	}
	a, err := d.claudeCTODeliverer()
	if err != nil {
		return nil, err
	}
	msgID, _ := ctx.Value(ctoMessageIDKey{}).(string)
	if msgID == "" {
		msgID = protocol.NewToken()
	}
	watch := &ctoTranscriptWatch{projects: filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects"), session: id, needle: []byte(msgID)}
	watch.start()
	deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		var res Result
		if label == "" {
			res = a.DeliverCTO(deadline, protocol.SessionInfo{Harness: "claude", NativeID: id}, msgID, text)
		} else {
			res = a.DeliverCTOSystem(deadline, protocol.SessionInfo{Harness: "claude", NativeID: id}, msgID, label, text)
		}
		if res.Delivered {
			d.confirmClaudeCTODelivery(watch, msgID, label, text)
			if err := d.touchCTOConversation("claude"); err != nil {
				return nil, err
			}
			return map[string]string{"session_id": id}, nil
		}
		select {
		case <-deadline.Done():
			return nil, fmt.Errorf("send to CTO Claude: %s", res.Detail)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// saveClaudeCTOSession stores the conversation the CTO's Claude runs now,
// which a resume can change from oldID, and moves the recorded actions to it.
func (d *Daemon) saveClaudeCTOSession(oldID, id string) error {
	if err := d.setCTOConversation("claude", id); err != nil {
		return err
	}
	_, err := d.st.db.Exec(`UPDATE cto_actions SET session_id=? WHERE (session_id='' OR session_id=?) AND harness='claude'`, id, oldID)
	return err
}

type claudeCTODeliverer interface {
	DeliverCTO(context.Context, protocol.SessionInfo, string, string) Result
	DeliverCTOSystem(context.Context, protocol.SessionInfo, string, string, string) Result
}

func (d *Daemon) claudeCTODeliverer() (claudeCTODeliverer, error) {
	a, ok := d.adapters["claude"].(claudeCTODeliverer)
	if !ok {
		return nil, errors.New("Claude adapter is not available")
	}
	return a, nil
}

func (d *Daemon) claudeCTOSession() (string, error) {
	return d.ctoConversation("claude")
}

// newConversation ends the Claude conversation. The gateway call runs without
// d.cto.mu (B12). It follows the retry rule of openCodeCTONew: the Claude run
// (root, session, model, ref) must be the one read before the call.
func (claudeCTOHarness) newConversation(ctx context.Context, d *Daemon) (any, error) {
	c := &d.claudeCTO
	for attempt := 1; ; attempt++ {
		d.cto.mu.Lock()
		session, model, ref, root := c.sessionID(), c.model, c.ref, c.root.Load()
		d.cto.mu.Unlock()

		data, err := d.call(ctx, "cto.graph_latest", nil)
		if err != nil {
			return nil, err
		}
		var latest struct {
			ID int64 `json:"id"`
		}
		if err = json.Unmarshal(data, &latest); err != nil {
			return nil, err
		}

		d.cto.mu.Lock()
		if c.sessionID() != session || c.model != model || c.ref != ref || c.root.Load() != root {
			d.cto.mu.Unlock()
			if attempt == 1 {
				continue
			}
			return nil, errors.New("the CTO conversation changed while a new one was starting; try again")
		}
		stop := d.detachClaudeCTOLocked("new_conversation")
		d.cto.mu.Unlock()
		stop.run()
		if err = d.resetClaudeCTO(latest.ID); err != nil {
			return nil, err
		}
		return struct{}{}, nil
	}
}

// resetClaudeCTO records the graph id that the next conversation starts after
// and drops the stored conversation. The run is already detached and stopped;
// ctoDispatchMu keeps a send from storing a conversation in between.
func (d *Daemon) resetClaudeCTO(graphAfter int64) error {
	tx, err := d.st.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM cto_actions WHERE session_id='' AND harness='claude'`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO cto(id,session_id,claude_graph_after) VALUES(1,'',?) ON CONFLICT(id) DO UPDATE SET claude_graph_after=excluded.claude_graph_after`, graphAfter); err != nil {
		return err
	}
	if err = setCTOConversationExec(tx, d.ctoNowTime(), "claude", ""); err != nil {
		return err
	}
	return tx.Commit()
}

func (claudeCTOHarness) events(ctx context.Context, d *Daemon, index int) (protocol.CTOEvents, error) {
	out := protocol.CTOEvents{Messages: []json.RawMessage{}, Actions: []protocol.CTOAction{}}
	// The settings come from the gateway, so they are read before cto.mu.
	settings, err := d.ctoSettings(ctx, protocol.CTOSettingsRequest{})
	if err != nil {
		return out, err
	}
	out.Model = settings.ClaudeModel
	id, err := d.claudeCTOSession()
	if err != nil {
		return out, err
	}
	if err = d.st.db.QueryRow(`SELECT claude_graph_after FROM cto WHERE id=1`).Scan(&out.GraphAfter); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	rows, err := d.st.db.Query(`SELECT id,text,at FROM cto_actions WHERE session_id=? AND harness='claude' ORDER BY id`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var a protocol.CTOAction
		if err = rows.Scan(&a.ID, &a.Text, &a.At); err != nil {
			rows.Close()
			return out, err
		}
		out.Actions = append(out.Actions, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if id == "" {
		return out, nil
	}
	messages, err := d.ctoTranscript.read(filepath.Join(filepath.Dir(d.cfg.ClaudeSessionsDir), "projects"), id)
	if err != nil {
		return out, err
	}
	out.Next = len(messages)
	if index >= 0 && index < len(messages) {
		out.Messages = messages[index:]
	}
	return out, nil
}

// claudeCTOTranscript reads the whole CTO transcript from the start. The daemon
// uses a ctoTranscriptCache instead, which gives the same answer without
// parsing the lines it has already seen.
func claudeCTOTranscript(projects, id string) ([]json.RawMessage, error) {
	return new(ctoTranscriptCache).read(projects, id)
}

// ctoTranscriptCache keeps the parsed messages of the CTO transcript. The file
// is only appended to, so a call parses only the lines added since the last
// one. It starts over when the file is another file, got shorter, or no longer
// has the bytes it had at the end of the last complete line. It looks at the
// file and never at the clock: the answer is what a full parse gives.
type ctoTranscriptCache struct {
	mu       sync.Mutex
	path     string
	info     os.FileInfo
	offset   int64  // end of the last complete line that was parsed
	tail     []byte // the bytes just before offset
	lastCmd  string // command markup state at offset
	messages []json.RawMessage
	parsed   int // lines parsed so far, for tests
}

// ctoTranscriptTailCheck is how many bytes before the offset are compared to
// tell an appended file from a rewritten one.
const ctoTranscriptTailCheck = 256

func (c *ctoTranscriptCache) reset() {
	c.path, c.info, c.offset, c.tail, c.lastCmd, c.messages = "", nil, 0, nil, "", nil
}

// sameTail tells whether the file still holds the bytes the cache saw before
// its offset.
func (c *ctoTranscriptCache) sameTail(f *os.File) bool {
	if len(c.tail) == 0 {
		return c.offset == 0
	}
	buf := make([]byte, len(c.tail))
	_, err := f.ReadAt(buf, c.offset-int64(len(buf)))
	return err == nil && bytes.Equal(buf, c.tail)
}

func (c *ctoTranscriptCache) read(projects, id string) ([]json.RawMessage, error) {
	empty := []json.RawMessage{}
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, "*?[]") {
		return empty, errors.New("invalid Claude session id")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var f *os.File
	var err error
	if c.path != "" && filepath.Dir(filepath.Dir(c.path)) == projects && filepath.Base(c.path) == id+".jsonl" {
		f, err = os.Open(c.path)
	}
	if f == nil || err != nil {
		matches, gerr := filepath.Glob(filepath.Join(projects, "*", id+".jsonl"))
		if gerr != nil || len(matches) == 0 {
			c.reset()
			return empty, gerr
		}
		if f, err = os.Open(matches[0]); err != nil {
			c.reset()
			return empty, err
		}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		c.reset()
		return empty, err
	}
	if c.info == nil || !os.SameFile(c.info, info) || info.Size() < c.offset || !c.sameTail(f) {
		c.reset()
	}
	c.path, c.info = f.Name(), info
	if info.Size() == c.offset {
		return c.messages[:len(c.messages):len(c.messages)], nil
	}

	// Lines that end in a newline are kept. A last line without one may still
	// be written to: it is part of this answer, but not of what is kept.
	lastCmd := c.lastCmd
	var fresh []json.RawMessage
	var consumed int64
	partial := false
	sc := bufio.NewScanner(io.NewSectionReader(f, c.offset, info.Size()-c.offset))
	sc.Buffer(make([]byte, 0, 1<<16), 16<<20)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		advance, token, err := bufio.ScanLines(data, atEOF)
		if token != nil {
			partial = advance == len(data) && data[len(data)-1] != '\n'
			if !partial {
				consumed += int64(advance)
			}
		}
		return advance, token, err
	})
	keptLen, keptCmd := 0, lastCmd
	for sc.Scan() {
		c.parsed++
		if m, ok := parseCTOTranscriptLine(sc.Bytes(), &lastCmd); ok {
			fresh = append(fresh, m)
		}
		if !partial {
			keptLen, keptCmd = len(fresh), lastCmd
		}
	}
	if err := sc.Err(); err != nil {
		// Same as a full parse: what was read, and the error.
		out := append(c.messages[:len(c.messages):len(c.messages)], fresh...)
		c.reset()
		return out, err
	}
	out := append(c.messages[:len(c.messages):len(c.messages)], fresh...)
	c.messages = append(c.messages, fresh[:keptLen]...)
	c.offset += consumed
	c.lastCmd = keptCmd
	c.tail = c.tail[:0]
	if c.offset > 0 {
		n := min(int64(ctoTranscriptTailCheck), c.offset)
		buf := make([]byte, n)
		if _, err := f.ReadAt(buf, c.offset-n); err != nil {
			c.reset()
			return out, nil
		}
		c.tail = buf
	}
	return out, nil
}

// parseCTOTranscriptLine turns one transcript line into a timeline message.
// lastCmd carries the command markup state from line to line.
func parseCTOTranscriptLine(raw []byte, lastCmd *string) (json.RawMessage, bool) {
	var line struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Side      bool   `json:"isSidechain"`
		Meta      bool   `json:"isMeta"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &line) != nil || line.Side || line.Meta || (line.Type != "user" && line.Type != "assistant") {
		return nil, false
	}
	var text string
	if json.Unmarshal(line.Message.Content, &text) != nil {
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(line.Message.Content, &parts) != nil {
			return nil, false
		}
		for _, p := range parts {
			if p.Type == "text" {
				text += p.Text
			}
		}
	}
	if strings.TrimSpace(text) == "" {
		return nil, false
	}
	if line.Type == "user" && strings.HasPrefix(text, "<cross-session-message ") {
		if _, body, ok := strings.Cut(text, ">\n"); ok {
			if owner, _, ok := strings.Cut(body, "\n\n[agent-net]"); ok {
				text = owner
			} else if prompt, ok := strings.CutSuffix(body, "\n</cross-session-message>"); ok && strings.HasPrefix(prompt, "[agent-net: ") {
				// agent-net's own labelled prompt, such as the handover request,
				// reads back as the form the timeline drops (ctoSystemPromptText).
				text = prompt
			}
		}
	}
	clean, newCmd, _ := claudetext.CleanCommandMarkup(text, *lastCmd)
	if newCmd != "" {
		*lastCmd = newCmd
	}
	text = clean
	if strings.TrimSpace(text) == "" {
		return nil, false
	}
	var millis int64
	if at, err := time.Parse(time.RFC3339Nano, line.Timestamp); err == nil {
		millis = at.UnixMilli()
	}
	b, _ := json.Marshal(map[string]any{"type": line.Type, "text": text, "time": map[string]int64{"created": millis}})
	return b, true
}

func (d *Daemon) bindClaudeCTO(sf claudeSessionFile, model, id, mode string, expectedGen uint64) bool {
	token, alive := procToken(sf.PID)
	if !alive {
		return false
	}
	d.cto.mu.Lock()
	if d.claudeCTO.stopping[sf.JobID] {
		// A stop outside cto.mu is ending this job and its process.
		d.cto.mu.Unlock()
		return false
	}
	if d.claudeCTO.gen != expectedGen {
		d.cto.mu.Unlock()
		(claudeLauncher{}).stopID(d, sf.JobID)
		_ = syscall.Kill(sf.PID, syscall.SIGKILL)
		return false
	}
	if mode == "adopted" && d.claudeCTO.root.Load() != nil {
		d.cto.mu.Unlock()
		return true
	}
	d.claudeCTO.ref, d.claudeCTO.model, d.claudeCTO.token = sf.JobID, model, token
	d.claudeCTO.setSession(sf.SessionID)
	d.claudeCTO.root.Store(&claudeCTORoot{pid: sf.PID, token: token})
	d.cto.mu.Unlock()
	d.Logf("cto.start harness=claude mode=%s", mode)
	d.scheduleCTOPresence()
	if d.ctoCtx != nil {
		d.goBackground(func() { d.monitorClaudeCTO(d.ctoCtx, sf.JobID, sf.PID, token) })
	}
	if id != "" && id != sf.SessionID && d.log != nil {
		d.log.Printf("CTO Claude resumed as a copy: %s -> %s", id, sf.SessionID)
	}
	return true
}

// adoptLiveClaudeCTO restores the process root before reconnecting MCP children.
func (d *Daemon) adoptLiveClaudeCTO() bool {
	if d.claudeCTO.starting.Load() {
		return false
	}
	d.cto.mu.Lock()
	bound := d.claudeCTO.root.Load() != nil
	gen := d.claudeCTO.gen
	d.cto.mu.Unlock()
	if bound {
		return false
	}
	sf, ok := findClaudeCTOBg(d.cfg.ClaudeSessionsDir, filepath.Join(d.cfg.StateDir, "cto", "claude"), "")
	if !ok {
		return false
	}
	status, _ := claudeCTOJobState(d.cfg.ClaudeSessionsDir, sf.JobID)
	if claudeJobEnded(status) {
		return false
	}
	models, _, err := d.localCTOModelSettings()
	if err != nil {
		return false
	}
	return d.bindClaudeCTO(sf, models.Claude, "", "adopted", gen)
}
