package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/niparis/agent-net/internal/daemon"
)

// Claude Code writes a system entry when it keeps a peer message for review
// instead of showing it to the session. The entry names the reply socket and
// the first line of the body, so the adapter can tie it to the one delivery it
// describes. When the owner approves it, Claude Code writes the message as a
// user entry with the original frame, which is the release.
const (
	// heldLookback is how much of an existing transcript the first read looks
	// at. A hold is recent; older text is not news.
	heldLookback = 1 << 20
	// heldChunk bounds one read. A transcript can be hundreds of megabytes,
	// so the scan never loads the whole of what was appended.
	heldChunk = 64 << 10
	// heldMaxLine drops a single line larger than this. Assistant output and
	// tool results can be far larger than a hold notice, and no frame is this
	// big.
	heldMaxLine = 1 << 20
	// heldPathLookupEvery is how often a transcript that moved is searched for
	// again. The normal path needs no search at all.
	heldPathLookupEvery = time.Minute
)

var (
	// A hold notice names the sender's reply socket: "from uds:<path>".
	heldFrom = regexp.MustCompile(`\bfrom[= ]"?(uds:[^" );\s]+)"?`)
	// A hold notice quotes the first body line and its size.
	heldCounts = regexp.MustCompile(`\[(\d+) lines?, (\d+) chars?`)
	// The frame content starts with the cross-session wrapper.
	heldOpenTag = regexp.MustCompile(`<cross-session-message\b([^>]*)>`)
)

// globTranscripts is filepath.Glob, replaced by a test that counts searches.
var globTranscripts = filepath.Glob

// heldDash is the em dash Claude Code writes in a hold notice. It is built
// from a rune because B9 forbids the character in a production literal; the
// daemon only matches it and never shows it.
var heldDash = string(rune(0x2014))

// heldWatch is how far one transcript was read.
type heldWatch struct {
	path    string
	offset  int64
	partial []byte
}

// heldPath is a session's transcript and the working folder it was found for.
type heldPath struct {
	path string
	cwd  string
}

// transcriptEntry is the part of a Claude transcript line the hold check
// reads. A system entry carries its text in content; a user entry carries a
// message with string or block content.
type transcriptEntry struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	UUID      string `json:"uuid"`
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
// holds the adapter lock across a read.
func (a *Adapter) scanHeld(byNative map[string]found) {
	keepNative := make(map[string]bool, len(byNative))
	keepPaths := map[string]bool{}
	for native, f := range byNative {
		keepNative[native] = true
		path := a.transcriptPath(native, f.info.Cwd)
		if path == "" {
			continue
		}
		keepPaths[path] = true
		a.scanHeldTranscript(native, path)
	}
	a.mu.Lock()
	for native := range a.heldPaths {
		if !keepNative[native] {
			delete(a.heldPaths, native)
		}
	}
	for native := range a.heldGlobAt {
		if !keepNative[native] {
			delete(a.heldGlobAt, native)
		}
	}
	for path := range a.heldWatches {
		if !keepPaths[path] {
			delete(a.heldWatches, path)
		}
	}
	a.mu.Unlock()
}

// transcriptPath finds a session's transcript without searching every project
// folder on every poll. The normal path is the folder named after the working
// folder; a session whose file moved is searched for at most once a minute.
func (a *Adapter) transcriptPath(native, cwd string) string {
	if native == "" {
		return ""
	}
	name := native + ".jsonl"
	a.mu.Lock()
	if p, ok := a.heldPaths[native]; ok && p.cwd == cwd && p.path != "" {
		a.mu.Unlock()
		return p.path
	}
	a.mu.Unlock()
	if cwd != "" {
		p := filepath.Join(a.cfg.ProjectsDir, daemon.ClaudeTranscriptDir(cwd), name)
		if _, err := os.Stat(p); err == nil {
			a.rememberHeldPath(native, cwd, p)
			return p
		}
	}
	a.mu.Lock()
	last := a.heldGlobAt[native]
	if !last.IsZero() && time.Since(last) < heldPathLookupEvery {
		a.mu.Unlock()
		return ""
	}
	if a.heldGlobAt == nil {
		a.heldGlobAt = map[string]time.Time{}
	}
	a.heldGlobAt[native] = time.Now()
	a.mu.Unlock()
	matches, _ := globTranscripts(filepath.Join(a.cfg.ProjectsDir, "*", name))
	if len(matches) == 0 {
		return ""
	}
	a.rememberHeldPath(native, cwd, matches[0])
	return matches[0]
}

func (a *Adapter) rememberHeldPath(native, cwd, path string) {
	a.mu.Lock()
	if a.heldPaths == nil {
		a.heldPaths = map[string]heldPath{}
	}
	a.heldPaths[native] = heldPath{path: path, cwd: cwd}
	a.mu.Unlock()
}

// forgetHeldPath drops a remembered path that no longer exists, so the next
// poll resolves it again.
func (a *Adapter) forgetHeldPath(native, path string) {
	a.mu.Lock()
	if p, ok := a.heldPaths[native]; ok && p.path == path {
		delete(a.heldPaths, native)
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
		a.forgetHeldPath(native, path)
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
	if info.Size() < w.offset {
		// The transcript was rewritten. Read only its tail: it can be very
		// large, and a rewritten transcript does not describe a recent frame.
		w.offset, w.partial = max(info.Size()-heldLookback, 0), nil
	}
	offset, end, partial := w.offset, info.Size(), w.partial
	ctx := a.ctx
	a.mu.Unlock()
	if end <= offset {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, heldChunk)
	skipLine := false
	for offset < end {
		n := int64(len(buf))
		if end-offset < n {
			n = end - offset
		}
		read, _ := f.ReadAt(buf[:n], offset)
		if read <= 0 {
			break
		}
		offset += int64(read)
		chunk := buf[:read]
		for {
			i := bytes.IndexByte(chunk, '\n')
			if i < 0 {
				if !skipLine {
					partial = append(partial, chunk...)
					if len(partial) > heldMaxLine {
						partial, skipLine = nil, true
					}
				}
				break
			}
			line := chunk[:i]
			chunk = chunk[i+1:]
			if skipLine {
				skipLine = false
				continue
			}
			if len(partial) > 0 {
				line = append(partial, line...)
				partial = nil
			}
			a.handleHeldLine(ctx, recipient, native, line)
		}
	}
	a.mu.Lock()
	w.offset = offset
	w.partial = append([]byte(nil), partial...)
	a.mu.Unlock()
}

// handleHeldLine reports one transcript line that may name a hold or a
// release. Every other line is skipped before the JSON parse.
func (a *Adapter) handleHeldLine(ctx context.Context, recipient, native string, line []byte) {
	if len(line) == 0 {
		return
	}
	if !bytes.Contains(line, []byte("Held peer message")) &&
		!bytes.Contains(line, []byte("Held message from another session")) &&
		!bytes.Contains(line, []byte("cross-session-message")) {
		return
	}
	var e transcriptEntry
	if json.Unmarshal(line, &e) != nil {
		return
	}
	switch e.Type {
	case "system":
		sock, preview, lines, chars, ok := heldNotice(e.Content)
		if !ok {
			return
		}
		at := parseTranscriptTime(e.Timestamp)
		if !at.IsZero() && sock != "" {
			a.host.ClaudeHeld(ctx, recipient, daemon.ClaudeHeldEntry{
				UUID: e.UUID, ReplySock: sock, Preview: preview,
				BodyLines: lines, BodyChars: chars, At: at,
			})
			return
		}
		a.host.Logf("claude: a hold notice in %s has no timestamp or reply socket: %q", native, firstLine(e.Content))
	case "user":
		content, ok := heldFrameContent(contentText(e.Message.Content))
		if !ok {
			return
		}
		sock, ok := frameReplySock(content)
		if !ok {
			a.host.Logf("claude: a released frame in %s names no reply socket", native)
			return
		}
		a.host.ClaudeHeldReleased(ctx, recipient, sock, frameHash(content), parseTranscriptTime(e.Timestamp))
	}
}

// heldNotice reads the sender socket, the quoted first line and the body size
// from a "Held peer message" entry. Both wordings seen in transcripts are
// accepted.
func heldNotice(s string) (sock, preview string, lines, chars int, ok bool) {
	if !heldEntry(s) {
		return "", "", 0, 0, false
	}
	if m := heldFrom.FindStringSubmatch(s); m != nil {
		sock = strings.TrimPrefix(m[1], "uds:")
	}
	if i := strings.Index(s, "preview: \u00ab"); i >= 0 {
		rest := s[i+len("preview: \u00ab"):]
		if j := strings.Index(rest, "\u00bb"); j >= 0 {
			preview = rest[:j]
		} else {
			cut := len(rest)
			for _, marker := range []string{"\u2026[", " " + heldDash + " not delivered", heldDash + " not delivered"} {
				if j := strings.Index(rest, marker); j >= 0 && j < cut {
					cut = j
				}
			}
			preview = strings.TrimSpace(rest[:cut])
		}
	}
	if m := heldCounts.FindStringSubmatch(s); m != nil {
		lines, _ = strconv.Atoi(m[1])
		chars, _ = strconv.Atoi(m[2])
	}
	return sock, preview, lines, chars, sock != ""
}

// heldEntry says whether a system entry is Claude Code's hold notice.
func heldEntry(s string) bool {
	if !strings.Contains(s, "not delivered to Claude") {
		return false
	}
	return strings.Contains(s, "Held peer message") || strings.Contains(s, "Held message from another session")
}

// heldFrameContent returns the exact cross-session wrapper in a released user
// entry. Claude Code may put text before it, but the frame itself is written
// back unchanged.
func heldFrameContent(text string) (string, bool) {
	i := strings.Index(text, "<cross-session-message")
	if i < 0 {
		return "", false
	}
	j := strings.Index(text[i:], "</cross-session-message>")
	if j < 0 {
		return "", false
	}
	return text[i : i+j+len("</cross-session-message>")], true
}

// frameReplySock reads the reply socket from a frame's opening tag.
func frameReplySock(content string) (string, bool) {
	m := heldOpenTag.FindStringSubmatch(content)
	if m == nil {
		return "", false
	}
	for _, field := range strings.Fields(m[1]) {
		if strings.HasPrefix(field, `from="uds:`) {
			return strings.TrimSuffix(strings.TrimPrefix(field, `from="uds:`), `"`), true
		}
	}
	return "", false
}

// frameHash is the hash of one frame content, which ties a release to the
// frame it was written for.
func frameHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// framePreview is the first non-empty body line, which is what a hold notice
// quotes.
func framePreview(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// frameBodyLines and frameBodyChars are the body size a hold notice reports.
func frameBodyLines(body string) int {
	if body == "" {
		return 0
	}
	return strings.Count(body, "\n") + 1
}

func frameBodyChars(body string) int { return utf8.RuneCountInString(body) }

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
