package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/niparis/agent-net/internal/protocol"
)

// modeOfKind maps the "kind" field of ~/.claude/sessions/<pid>.json to a mode.
// Claude Code 2.1.312 writes one of "interactive", "bg", "daemon" and
// "daemon-worker" (they are the values its session file schema accepts).
// Only "interactive" is a session a person has open in a terminal or app.
// A file without a kind (an older Claude Code) gives "unknown".
func modeOfKind(kind string) string {
	switch kind {
	case "":
		return protocol.ModeUnknown
	case "interactive":
		return protocol.ModeForeground
	}
	return protocol.ModeBackground
}

// ---- model from the transcript ----

// Reading the model costs one stat per session per poll. The file is only
// read again when its size or time changed, and then only from the end, in
// blocks, until the last assistant entry is found.
const (
	tailBlock    = 64 << 10
	tailMaxBytes = 8 << 20
)

var dirBad = regexp.MustCompile(`[^A-Za-z0-9]`)

// transcriptDir is the folder name Claude Code uses for a working folder: every
// character that is not a letter or digit becomes "-".
func transcriptDir(cwd string) string { return dirBad.ReplaceAllString(cwd, "-") }

type modelEntry struct {
	path  string
	size  int64
	mtime time.Time
	model string
}

// modelReader finds the model of a Claude Code session. It reads only the
// "model" field of the last assistant entry of the transcript and keeps no
// transcript text.
type modelReader struct {
	projects string // ~/.claude/projects

	mu    sync.Mutex
	cache map[string]modelEntry // session ID -> last result
}

func newModelReader(projectsDir string) *modelReader {
	return &modelReader{projects: projectsDir, cache: map[string]modelEntry{}}
}

// transcript returns the path of the session's transcript, or "".
func (r *modelReader) transcript(sessionID, cwd string) string {
	name := sessionID + ".jsonl"
	if cwd != "" {
		p := filepath.Join(r.projects, transcriptDir(cwd), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// The folder name rule may differ in another Claude Code version, or the
	// session may have moved. Look in every project folder.
	m, _ := filepath.Glob(filepath.Join(r.projects, "*", name))
	if len(m) > 0 {
		return m[0]
	}
	return ""
}

// Model returns the model of the session, or "" when the transcript is missing
// or has no assistant entry yet. It keeps the last known model if a read fails.
func (r *modelReader) Model(sessionID, cwd string) string {
	r.mu.Lock()
	prev, had := r.cache[sessionID]
	r.mu.Unlock()
	path := prev.path
	if path == "" {
		path = r.transcript(sessionID, cwd)
	}
	if path == "" {
		return prev.model
	}
	fi, err := os.Stat(path)
	if err != nil {
		// Gone or moved: look again next time.
		r.mu.Lock()
		delete(r.cache, sessionID)
		r.mu.Unlock()
		return prev.model
	}
	if had && fi.Size() == prev.size && fi.ModTime().Equal(prev.mtime) {
		return prev.model
	}
	model, found := lastModel(path, fi.Size())
	if !found {
		model = prev.model
	}
	r.mu.Lock()
	r.cache[sessionID] = modelEntry{path: path, size: fi.Size(), mtime: fi.ModTime(), model: model}
	r.mu.Unlock()
	return model
}

// forget drops the cache of sessions that are not in keep.
func (r *modelReader) forget(keep map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.cache {
		if !keep[id] {
			delete(r.cache, id)
		}
	}
}

// lastModel reads path from the end and returns the "model" of the newest
// assistant entry that has one. Entries of sub-agents (isSidechain) and
// Claude Code's own placeholder model ("<synthetic>") are skipped.
func lastModel(path string, size int64) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	// tail holds the bytes from offset to the end of what has been read. The
	// first line in it may be cut off, so it is only used when a newline
	// comes before it or the start of the file was reached.
	var tail []byte
	end := size
	block := int64(tailBlock)
	for read := int64(0); end > 0 && read < tailMaxBytes; {
		start := max(end-min(block, tailMaxBytes-read), 0)
		block *= 2 // a long line needs fewer rounds
		buf := make([]byte, end-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return "", false
		}
		read += int64(len(buf))
		tail = append(buf, tail...)
		end = start
		lines := bytes.Split(tail, []byte("\n"))
		first := 0
		if end > 0 {
			first = 1 // lines[0] may be cut off; the next block completes it
		}
		for i := len(lines) - 1; i >= first; i-- {
			if m, ok := modelOfLine(lines[i]); ok {
				return m, true
			}
		}
		if end > 0 {
			// Keep only the unfinished first line for the next round.
			tail = lines[0]
		}
	}
	return "", false
}

var assistantMark = []byte(`"assistant"`)

// modelOfLine reads the model of one transcript line if it is an assistant
// entry of the main conversation.
func modelOfLine(line []byte) (string, bool) {
	if !bytes.Contains(line, assistantMark) {
		return "", false
	}
	var e struct {
		Type        string `json:"type"`
		IsSidechain bool   `json:"isSidechain"`
		Message     struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &e) != nil || e.Type != "assistant" || e.IsSidechain {
		return "", false
	}
	m := e.Message.Model
	if m == "" || m[0] == '<' {
		return "", false
	}
	return protocol.CleanModel(m), true
}
